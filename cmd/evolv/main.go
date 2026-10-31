// evolv — the self-evolving AI workspace.
// One command. Browser opens. The organism boots and starts mutating.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dxn111/EVOLV/assets"
	"github.com/dxn111/EVOLV/internal/agent"
	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/providers"
	"github.com/dxn111/EVOLV/internal/server"
	"github.com/dxn111/EVOLV/internal/watcher"
	"github.com/dxn111/EVOLV/internal/workspace"
)

const banner = `
  ███████╗██╗   ██╗ ██████╗ ██╗    ██╗   ██╗
  ██╔════╝██║   ██║██╔═══██╗██║    ██║   ██║
  █████╗  ██║   ██║██║   ██║██║    ██║   ██║
  ██╔══╝  ╚██╗ ██╔╝██║   ██║██║    ██║   ██║
  ███████╗ ╚████╔╝ ╚██████╔╝███████╗╚██████╔╝
  ╚══════╝  ╚═══╝   ╚═════╝ ╚══════╝ ╚═════╝
        the workspace that rewrites itself
`

func main() {
	var (
		port    = flag.Int("port", 0, "port to serve on (0 = config/auto)")
		noOpen  = flag.Bool("no-open", false, "do not open the browser")
		version = flag.Bool("version", false, "print version")
		reset   = flag.Bool("reset", false, "reset workspace UI to factory genome")
	)
	flag.Parse()

	if *version {
		fmt.Println("evolv", workspace.Version)
		return
	}
	fmt.Print(banner)
	fmt.Printf("   v%s · github.com/dxn111/EVOLV · %s\n\n", workspace.Version, time.Now().Format("2006-01-02 15:04:05"))

	ws, err := workspace.Load(assets.FS)
	if err != nil {
		log.Fatalf("workspace: %v", err)
	}
	if *reset {
		_ = os.RemoveAll(ws.Dir(workspace.UIRel()))
		if _, err := workspace.Load(assets.FS); err != nil {
			log.Fatalf("reseed: %v", err)
		}
		fmt.Println("✓ genome reset to factory UI")
	}

	b := bus.New()
	w := watcher.New(ws.Root, b)
	w.Start()

	prov, model := providers.Build(ws.Config.Provider, ws.Config.Model, ws.Config.APIKeys[ws.Config.Provider], ws.Config.BaseURLs)
	ws.Config.Model = model
	ag := agent.New(ws, b, prov, model)

	// registry so the UI can see every provider family
	reg := providers.NewRegistry()
	for _, name := range []string{"kilo", "openai", "anthropic", "openrouter", "groq", "ollama", "custom", "mock"} {
		p, _ := providers.Build(name, "", ws.Config.APIKeys[name], ws.Config.BaseURLs)
		reg.Add(p)
	}

	srv := server.New(ws, b, ag, reg)
	p := ws.Config.Port
	if *port > 0 {
		p = *port
	}
	if p == 0 {
		p = 7373
	}
	portOut, ln, err := srv.Listen(p)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	url := fmt.Sprintf("http://localhost:%d", portOut)
	fmt.Printf("  ✓ organism online      %s\n", url)
	fmt.Printf("  ✓ provider             %s · %s\n", prov.Name(), model)
	fmt.Printf("  ✓ workspace            %s\n", ws.Root)
	fmt.Println("  ✓ watching for mutations (live hot-reload)")
	fmt.Println()

	headless := os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
	if ws.Config.OpenBrowser && !*noOpen && !headless {
		go func() {
			time.Sleep(400 * time.Millisecond)
			workspace.OpenBrowser(url)
		}()
	} else if headless {
		fmt.Println("  (headless session — open the URL above manually)")
	}

	go func() {
		if err := http.Serve(ln, srv.Handler()); err != nil {
			log.Fatalf("serve: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	fmt.Println("\n  organism paused. genome preserved at", ws.Root)
}

var _ = json.Marshal
var _ = strings.TrimSpace
