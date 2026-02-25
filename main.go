package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var (
	flagPort      int
	flagNoBrowser bool
)

var rootCmd = &cobra.Command{
	Use:   "tp-gui",
	Short: "Telepresence web UI",
	Long:  "tp-gui starts a local web UI for managing Telepresence intercepts.",
	RunE:  run,
}

func init() {
	rootCmd.Flags().IntVarP(&flagPort, "port", "p", 7777, "port to listen on")
	rootCmd.Flags().BoolVar(&flagNoBrowser, "no-browser", false, "don't open the browser automatically")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	if !TpBinaryExists() {
		fmt.Fprintln(os.Stderr, "⚠  'telepresence' binary not found on PATH.")
		fmt.Fprintln(os.Stderr, "   Install it: https://www.telepresence.io/docs/install")
		fmt.Fprintln(os.Stderr, "   tp-gui will still start — some features won't work.")
	}

	// Pick a free port if the requested one is busy
	port, err := findPort(flagPort)
	if err != nil {
		return fmt.Errorf("no available port: %w", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s", addr)

	// Background context — cancelled on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the background poller
	go startPoller(ctx)

	// Seed the snapshot immediately so first SSE connection gets data fast
	go func() {
		time.Sleep(500 * time.Millisecond)
		pollAndBroadcast(ctx)
	}()

	srv := &http.Server{
		Addr:         addr,
		Handler:      newRouter(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // 0 = no timeout (needed for SSE)
		IdleTimeout:  120 * time.Second,
	}

	// Start HTTP server
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	fmt.Printf("✓ tp-gui running at %s\n", url)
	fmt.Println("  Press Ctrl+C to stop.")

	if !flagNoBrowser {
		// Small delay so the server is ready
		time.Sleep(300 * time.Millisecond)
		openBrowser(url)
	}

	<-ctx.Done()
	fmt.Println("\nShutting down…")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)

	return nil
}

// findPort returns the requested port if available, otherwise finds a free one.
func findPort(preferred int) (int, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
	if err == nil {
		_ = ln.Close()
		return preferred, nil
	}
	// Fall back to any free port
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

// openBrowser opens the URL in the default system browser.
func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	if err != nil {
		fmt.Printf("  (couldn't open browser: %v – navigate to %s manually)\n", err, url)
	}
}
