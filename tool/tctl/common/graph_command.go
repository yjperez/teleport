package common

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/gravitational/kingpin"
	"github.com/gravitational/teleport/lib/auth"
	"github.com/gravitational/teleport/lib/defaults"
	"github.com/gravitational/teleport/lib/reversetunnel"
	"github.com/gravitational/teleport/lib/service"
	"github.com/gravitational/teleport/lib/services"
	"github.com/gravitational/trace"
)

type GraphCommand struct {
	config *service.Config

	graph *kingpin.CmdClause
}

func (c *GraphCommand) Initialize(app *kingpin.Application, config *service.Config) {
	c.config = config
	c.graph = app.Command("graph", "Generate teleport dot-graph")
}

func (c *GraphCommand) TryRun(cmd string, client auth.ClientI) (match bool, err error) {
	switch cmd {
	case c.graph.FullCommand():
		err = c.Graph(client)
	default:
		return false, nil
	}
	return true, trace.Wrap(err)
}

type graphNode struct {
	services []services.Server
}

func (c *GraphCommand) Graph(client auth.ClientI) error {
	ctx := context.Background()

	var allServers []services.Server
	auths, err := client.GetAuthServers()
	if err != nil {
		return trace.Wrap(err)
	}
	allServers = append(allServers, auths...)
	proxies, err := client.GetProxies()
	if err != nil {
		return trace.Wrap(err)
	}
	allServers = append(allServers, proxies...)
	sshNodes, err := client.GetNodes(defaults.Namespace)
	if err != nil {
		return trace.Wrap(err)
	}
	allServers = append(allServers, sshNodes...)
	kubes, err := client.GetKubeServices(ctx)
	if err != nil {
		return trace.Wrap(err)
	}
	allServers = append(allServers, kubes...)
	apps, err := client.GetAppServers(ctx, defaults.Namespace)
	if err != nil {
		return trace.Wrap(err)
	}
	allServers = append(allServers, apps...)

	graph := make(map[string]*graphNode)
	for _, s := range allServers {
		n, ok := graph[s.GetName()]
		if !ok {
			n = &graphNode{}
		}
		n.services = append(n.services, s)
		graph[s.GetName()] = n
	}

	c.printGraph(graph)
	return nil
}

func (c *GraphCommand) printGraph(graph map[string]*graphNode) {
	fmt.Println("digraph G {")
	defer fmt.Println("}")

	//TODO: this may not be needed.
	svcAddrs := make(map[string]string)

	var authNodes, proxyNodes []string

	for instID, n := range graph {
		id := "cluster_" + instID
		fmt.Printf("    subgraph %q {\n", id)
		fmt.Printf("        label = %q;\n", instID)
		fmt.Printf("        color = gray;\n")

		for _, s := range n.services {
			sid := fmt.Sprintf("%s_%s", id, s.GetKind())
			var saddr string
			if s.GetPublicAddr() != "" {
				svcAddrs[s.GetPublicAddr()] = sid
				saddr = s.GetPublicAddr()
			}
			if s.GetHostname() != "" {
				_, port, err := net.SplitHostPort(s.GetAddr())
				if err == nil {
					addr := net.JoinHostPort(s.GetHostname(), port)
					svcAddrs[addr] = sid
					if saddr == "" {
						saddr = addr
					}
				}
			}
			if saddr == "" {
				saddr = s.GetAddr()
			}
			label := fmt.Sprintf("%s\n%s", s.GetKind(), saddr)
			var color string
			switch s.GetKind() {
			case services.KindAuthServer:
				color = "red"
			case services.KindProxy:
				color = "yellow"
			default:
				color = "white"
			}
			fmt.Printf("        %q [label=%q,peripheries=1,style=filled,fillcolor=%s];\n", sid, label, color)

			switch s.GetKind() {
			case services.KindAuthServer:
				authNodes = append(authNodes, sid)
			case services.KindProxy:
				proxyNodes = append(proxyNodes, sid)
			}
		}
		fmt.Printf("    }\n")

		for _, s := range n.services {
			sid := fmt.Sprintf("%s_%s", id, s.GetKind())
			if s.GetKind() != services.KindKubeService {
				continue
			}
			for _, kube := range s.GetKubernetesClusters() {
				kubeID := "kube_" + kube.Name
				fmt.Printf("    %q [shape=polygon,sides=7,color=blue,style=filled,fontcolor=white,label=%q];\n", kubeID, kube.Name)
				fmt.Printf("    %q -> %q;\n", sid, kubeID)
			}
		}
	}

	for instID, n := range graph {
		id := "cluster_" + instID
		for _, s := range n.services {
			sid := fmt.Sprintf("%s_%s", id, s.GetKind())
			switch s.GetKind() {
			case services.KindProxy:
				if hasLocalService(n, services.KindAuthServer) {
					fmt.Printf("    %q -> \"%s_%s\"\n", sid, id, services.KindAuthServer)
				} else {
					for _, auth := range authNodes {
						fmt.Printf("    %q -> %q\n", sid, auth)
					}
				}
			case services.KindAuthServer:
				// No outbound links.
			case services.KindNode:
				if s.GetUseTunnel() {
					for _, proxy := range proxyNodes {
						fmt.Printf("    %q -> %q\n", sid, proxy)
					}
				} else {
					for _, proxy := range proxyNodes {
						fmt.Printf("    %q -> %q\n", proxy, sid)
					}
				}
			case services.KindKubeService:
				if s.GetAddr() == reversetunnel.LocalKubernetes {
					for _, proxy := range proxyNodes {
						fmt.Printf("    %q -> %q\n", sid, proxy)
					}
				} else {
					for _, proxy := range proxyNodes {
						fmt.Printf("    %q -> %q\n", proxy, sid)
					}
				}
			case services.KindAppServer:
				// TODO
			default:
				fmt.Fprintf(os.Stderr, "unhandled service kind %q in graph linkage\n", s.GetKind())
			}
		}
	}
}

func hasLocalService(n *graphNode, kind string) bool {
	for _, s := range n.services {
		if s.GetKind() == kind {
			return true
		}
	}
	return false
}
