package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/lxliu/codex-session-viewer/internal/server"
	"github.com/lxliu/codex-session-viewer/internal/session"
	"github.com/lxliu/codex-session-viewer/web"
)

const version = "1.0.0"

func main() {
	port := flag.Int("port", 10002, "本机监听端口，0 表示自动选择")
	root := flag.String("codex-home", "", "Codex 根目录（默认 CODEX_HOME 或 ~/.codex）")
	data := flag.String("data-dir", "", "索引缓存目录（默认项目 data/）")
	ver := flag.Bool("version", false, "显示版本")
	flag.Parse()
	if *ver {
		fmt.Println("codex-session-viewer", version)
		return
	}
	debug.SetMemoryLimit(768 << 20)
	debug.SetGCPercent(75)
	home, e := os.UserHomeDir()
	if e != nil {
		log.Fatal(e)
	}
	if *root == "" {
		*root = os.Getenv("CODEX_HOME")
		if *root == "" {
			*root = filepath.Join(home, ".codex")
		}
	}
	if *data == "" {
		exe, _ := os.Executable()
		dir := filepath.Dir(exe)
		if filepath.Base(dir) == "bin" {
			*data = filepath.Join(filepath.Dir(dir), "data")
		} else {
			*data = "data"
		}
	}
	logDir := filepath.Join(filepath.Dir(*data), "log")
	if e := os.MkdirAll(logDir, 0700); e != nil {
		log.Fatal(e)
	}
	logFile, e := os.OpenFile(filepath.Join(logDir, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		log.Fatal(e)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	store, e := session.New(*root, *data)
	if e != nil {
		log.Fatal(e)
	}
	defer store.Close()
	ln, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if e != nil {
		log.Fatal(e)
	}
	srv := &http.Server{Handler: server.New(store, web.Files).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	addr := "http://" + ln.Addr().String()
	log.Printf("codex-session-viewer %s · %s", version, addr)
	log.Printf("只读来源 %s；缓存 %s", store.Root, store.Data)
	os.WriteFile(filepath.Join(store.Data, "server.port"), []byte(fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)), 0600)
	go func() {
		if e := srv.Serve(ln); e != nil && e != http.ErrServerClosed {
			log.Printf("serve: %v", e)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
