// Command server 启动“光回波事件室”本地服务。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"otdrroom/internal/app"
	"otdrroom/internal/store"
	"otdrroom/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5540", "HTTP 监听地址")
	dbPath := flag.String("db", "otdrroom.db", "SQLite 数据库路径")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	svc := app.New(st)
	// 空库时自动播种固定夹具；非空则保留现场。
	if err := svc.SeedFixtures(ctx); err != nil {
		log.Fatalf("导入固定夹具失败: %v", err)
	}

	srv, err := web.NewServer(svc)
	if err != nil {
		log.Fatalf("构造 HTTP 服务失败: %v", err)
	}

	log.Printf("光回波事件室已启动：http://%s", *listen)
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
