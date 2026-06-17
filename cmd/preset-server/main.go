// Command preset-server runs the minimal Bose SoundTouch preset emulator: it
// serves the six preset buttons as hardcoded internet-radio streams, reading
// its station list from a USB stick (with a persistent on-device cache).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rocketnewton/bose-presets/internal/preset"
)

func main() {
	var (
		listen      = flag.String("listen", ":8000", "address to listen on")
		baseURL     = flag.String("base-url", "http://127.0.0.1:8000", "absolute base URL the speaker uses to reach this service (embedded in preset locations)")
		usbPath     = flag.String("usb-config", "/media/sda1/presets.conf", "config file path on the USB stick")
		cachePath   = flag.String("cache", "/mnt/nv/presets.conf", "persistent cache config path")
		mountWait   = flag.Duration("mount-wait", 30*time.Second, "how long to wait for the USB mount at startup")
		reloadEvery = flag.Duration("reload-interval", 15*time.Second, "how often to re-read the config for changes (0 disables)")
	)
	flag.Parse()

	// Give udev time to mount the USB stick (async, races startup).
	if preset.WaitForFile(*usbPath, *mountWait, 500*time.Millisecond) {
		log.Printf("usb config present at %s", *usbPath)
	} else {
		log.Printf("usb config not found within %s; will try cache %s", *mountWait, *cachePath)
	}

	cfg, source, err := preset.Load(*usbPath, *cachePath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	log.Printf("loaded %d station(s) from %s", len(cfg.Stations), source)

	srv := preset.NewServer(*baseURL, cfg)

	if *reloadEvery > 0 {
		go reloadLoop(srv, *usbPath, *cachePath, *reloadEvery, preset.Fingerprint(cfg))
	}

	httpServer := &http.Server{
		Addr:         *listen,
		Handler:      srv.Handler(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		log.Printf("listening on %s (base-url %s)", *listen, *baseURL)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	log.Print("shut down")
}

// reloadLoop periodically reloads the config so an edited USB stick takes
// effect without a restart. Only the in-memory config is swapped; if the new
// config fails to parse, the running config is kept (Load returns an error and
// we skip the swap).
func reloadLoop(srv *preset.Server, usbPath, cachePath string, every time.Duration, initialFP string) {
	t := time.NewTicker(every)
	defer t.Stop()
	last := initialFP
	for range t.C {
		cfg, source, err := preset.Load(usbPath, cachePath)
		if err != nil {
			continue
		}
		fp := preset.Fingerprint(cfg)
		if fp == last {
			continue
		}
		last = fp
		srv.SetConfig(cfg)
		log.Printf("reloaded %d station(s) from %s", len(cfg.Stations), source)
	}
}
