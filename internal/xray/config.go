package xray

import (
	"encoding/json"
	"fmt"

	"github.com/faddey/xray-monitor/internal/subscription"
)

type RuntimeNode struct {
	Node      subscription.Node
	LocalPort int
}

func BuildConfig(nodes []RuntimeNode, listenIP, logLevel string) ([]byte, error) {
	type tagged map[string]any
	inbounds := make([]tagged, 0, len(nodes))
	// The first outbound is Xray's fallback for traffic without a matching rule.
	// Make that fallback fail closed instead of leaking traffic directly or via node 0.
	outbounds := []tagged{{"tag": "blocked", "protocol": "blackhole"}}
	rules := make([]tagged, 0, len(nodes))
	for i, runtimeNode := range nodes {
		node := runtimeNode.Node
		inboundTag := fmt.Sprintf("monitor-in-%d", i)
		outboundTag := fmt.Sprintf("monitor-out-%d", i)
		inbounds = append(inbounds, tagged{
			"tag":      inboundTag,
			"listen":   listenIP,
			"port":     runtimeNode.LocalPort,
			"protocol": "socks",
			"settings": tagged{"auth": "noauth", "udp": false},
		})
		user := tagged{"id": node.UUID, "encryption": "none"}
		if node.Flow != "" {
			user["flow"] = node.Flow
		}
		reality := tagged{
			"show":        false,
			"serverName":  node.ServerName,
			"fingerprint": node.Fingerprint,
			"publicKey":   node.PublicKey,
			"shortId":     node.ShortID,
		}
		if node.SpiderX != "" {
			reality["spiderX"] = node.SpiderX
		}
		outbounds = append(outbounds, tagged{
			"tag":      outboundTag,
			"protocol": "vless",
			"settings": tagged{
				"vnext": []any{tagged{
					"address": node.Address,
					"port":    node.Port,
					"users":   []any{user},
				}},
			},
			"streamSettings": tagged{
				"network":         node.Network,
				"security":        node.Security,
				"realitySettings": reality,
			},
		})
		rules = append(rules, tagged{
			"type":        "field",
			"inboundTag":  []string{inboundTag},
			"outboundTag": outboundTag,
		})
	}
	root := tagged{
		"log":       tagged{"loglevel": logLevel},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing": tagged{
			"domainStrategy": "AsIs",
			"rules":          rules,
		},
	}
	return json.MarshalIndent(root, "", "  ")
}
