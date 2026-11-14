package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/joho/godotenv/autoload"
	"github.com/taufik/iso-connector-revarm/internal/connector"
	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

func main() {
	mode := getenv("MODE", "both")
	packagerPath := getenv("PACKAGER_PATH", "cfg/packager/iso_spec.xml")
	packager, err := iso8583.LoadPackager(packagerPath)
	if err != nil {
		log.Fatalf("load ISO packager: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Print("MODE: " + mode)

	if mode == "server" || mode == "both" {
		addr := getenv("ISO_LISTEN_ADDR", ":"+getenv("ISO_PORT", "8989"))
		server, err := connector.NewServer(connector.ServerConfig{ListenAddr: addr, MessageURL: os.Getenv("MESSAGE_URL"), Packager: packager})
		if err != nil {
			log.Fatalf("start ISO server: %v", err)
		}
		go func() {
			if err := server.Serve(ctx); err != nil {
				log.Printf("ISO server stopped: %v", err)
			}
		}()
		log.Printf("ISO server listening on %s", server.Addr())
	}
	if mode == "client" || mode == "both" {
		client := connector.NewClient(connector.ClientConfig{Address: getenv("SERVER_HOST", "127.0.0.1") + ":" + getenv("SERVER_PORT", "8989"), Timeout: duration("NETWORK_TIMEOUT", 60*time.Second), HandshakeTimeout: duration("INITIAL_NETWORK_HANDSHAKE_TIMEOUT", 250*time.Millisecond), Packager: packager})
		if err := client.Start(ctx); err != nil {
			log.Fatalf("start persistent ISO client: %v", err)
		}
		httpServer := &http.Server{Addr: getenv("HTTP_LISTEN_ADDR", ":8085"), Handler: connector.HTTPHandler(client), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(shutdownCtx)
		}()
		log.Printf("HTTP client API listening on %s; POST /call", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server: %v", err)
		}
		return
	}
	<-ctx.Done()
}
func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func duration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err == nil {
		return d
	}
	if milliseconds, err := time.ParseDuration(value + "ms"); err == nil {
		return milliseconds
	}
	return fallback
}
