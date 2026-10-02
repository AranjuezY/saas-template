// Command server 是模板的装配根：单进程同时跑 HTTP 与两个 Temporal worker
// （开发友好形态）。拆分为独立 worker 进程时，把下面三段分别搬进
// cmd/webserver、cmd/workflow-worker、cmd/resources-worker 即可，
// 依赖关系完全不变。
//
//	./server              # 默认 :8090，SQLite 落在 data/app.db
//	ADDR=:9000 TEMPORAL_ADDRESS=localhost:7233 ./server
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.temporal.io/sdk/worker"

	"github.com/AranjuezY/saas-template/internal/example/contract"
	itemactivity "github.com/AranjuezY/saas-template/internal/example/resources/item/activity"
	itemhandler "github.com/AranjuezY/saas-template/internal/example/resources/item/handler"
	itemstore "github.com/AranjuezY/saas-template/internal/example/resources/item/store"
	itemview "github.com/AranjuezY/saas-template/internal/example/resources/item/view"
	exampleworkflow "github.com/AranjuezY/saas-template/internal/example/workflow"
	"github.com/AranjuezY/saas-template/internal/shared/db"
	"github.com/AranjuezY/saas-template/internal/shared/temporalclient"
	"github.com/AranjuezY/saas-template/internal/shared/ui"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if err := run(); err != nil {
		slog.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run() error {
	addr := env("ADDR", ":8090")
	dbPath := env("DB_PATH", "data/app.db")
	temporalAddr := env("TEMPORAL_ADDRESS", "localhost:7233")
	namespace := env("TEMPORAL_NAMESPACE", "default")

	// Ctrl+C / SIGTERM 触发优雅退出。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 打开数据库（建目录、建库、执行迁移）。
	database, err := db.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	// 连接 Temporal，并在进程内跑两个 worker（见文件头注释：拆分方式）。
	tc, err := temporalclient.Dial(temporalAddr, namespace)
	if err != nil {
		return err
	}
	defer tc.Close()

	wfWorker := worker.New(tc, contract.TaskQueueWorkflows, worker.Options{})
	exampleworkflow.RegisterAll(wfWorker)
	if err := wfWorker.Start(); err != nil {
		return err
	}
	defer wfWorker.Stop()

	resWorker := worker.New(tc, itemactivity.TaskQueue, worker.Options{})
	itemactivity.New(database).Register(resWorker)
	if err := resWorker.Start(); err != nil {
		return err
	}
	defer resWorker.Stop()

	// HTTP：item 域路由 + 首页（整页）+ 健康检查 + 404。
	st := itemstore.New(database)
	orch := exampleworkflow.NewClient(tc, 10*time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		list, err := st.List(r.Context())
		if err != nil {
			http.Error(w, "加载条目失败", http.StatusInternalServerError)
			return
		}
		ui.Render(w, r, http.StatusOK, itemview.Page(list))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	itemhandler.New(st, orch).Register(mux)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		ui.Render(w, r, http.StatusNotFound, itemview.NotFoundPage())
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           middleware(mux),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", addr, "db", dbPath,
			"temporal", temporalAddr, "namespace", namespace)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	slog.Info("server stopped")
	return nil
}

// middleware 输出访问日志并兜住 panic（装配根职责）。
func middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start).Round(time.Microsecond).String(),
		)
	})
}
