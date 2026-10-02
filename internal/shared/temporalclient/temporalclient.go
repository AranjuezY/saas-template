// Package temporalclient 是 Temporal 连接的共享适配器。
// 只有 cmd 装配根依赖本包建立连接；各域的编排客户端接收注入的连接。
package temporalclient

import (
	"fmt"

	"go.temporal.io/sdk/client"
)

// Dial 按显式参数建立到 Temporal Server 的客户端连接。
//
// 连接参数由 cmd 从配置读出后传入——本包不依赖配置形态。
// 开发环境通常是 `temporal server start-dev`（默认 localhost:7233）。
func Dial(hostPort, namespace string) (client.Client, error) {
	c, err := client.Dial(client.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("dial temporal %s: %w", hostPort, err)
	}
	return c, nil
}
