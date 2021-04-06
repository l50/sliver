package command

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bishopfox/sliver/client/core"
	"github.com/bishopfox/sliver/client/tcpproxy"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/desertbit/grumble"
)

func portfwd(ctx *grumble.Context, rpc rpcpb.SliverRPCClient) {
	outputBuf := bytes.NewBufferString("")
	table := tabwriter.NewWriter(outputBuf, 0, 2, 2, ' ', 0)
	fmt.Fprintf(table, "ID\tSession ID\tBind Address\tRemote Address\t\n")
	fmt.Fprintf(table, "%s\t%s\t%s\t%s\t\n",
		strings.Repeat("=", len("ID")),
		strings.Repeat("=", len("Session ID")),
		strings.Repeat("=", len("Bind Address")),
		strings.Repeat("=", len("Remote Address")),
	)
	for _, p := range core.Portfwds.List() {
		fmt.Fprintf(table, "%d\t%d\t%s\t%s\t\n",
			p.ID, p.SessionID, p.BindAddr, p.RemoteAddr)
	}
	table.Flush()
	fmt.Printf("\n%s\n", outputBuf.String())
}

func portfwdAdd(ctx *grumble.Context, rpc rpcpb.SliverRPCClient) {
	session := ActiveSession.GetInteractive()
	if session == nil {
		return
	}
	if session.GetActiveC2() == "dns" {
		fmt.Printf(Warn + "Current C2 is DNS, this is going to be a very slow tunnel!\n")
	}
	remoteAddr := ctx.Flags.String("remote")
	if remoteAddr == "" {
		fmt.Println(Warn + "Must specify a remote target host:port")
		return
	}
	remoteHost, remotePort, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		fmt.Print(Warn+"Failed to parse remote target %s\n", err)
		return
	}
	bindAddr := "127.0.0.1:8080"

	fmt.Printf(Info+"Port forwarding %s -> %s:%s\n", bindAddr, remoteHost, remotePort)

	tcpProxy := &tcpproxy.Proxy{}
	channelProxy := &core.ChannelProxy{
		Rpc:             rpc,
		Session:         session,
		RemoteAddr:      remoteAddr,
		BindAddr:        bindAddr,
		KeepAlivePeriod: 60 * time.Second,
		DialTimeout:     30 * time.Second,
	}
	tcpProxy.AddRoute(bindAddr, channelProxy)
	core.Portfwds.Add(tcpProxy, channelProxy)

	go func() {
		err := tcpProxy.Run()
		if err != nil {
			fmt.Printf("\r\n"+Warn+"Proxy error %s\n", err)
		}
	}()

	fmt.Println(Info + "Started proxy!")
}

func portfwdRm(ctx *grumble.Context, rpc rpcpb.SliverRPCClient) {
	// TODO
}
