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

	"github.com/jrnewton/bose-radio/internal/preset"
)

func main() {
	var (
		listen      = flag.String("listen", ":8000", "address to listen on")
		baseURL     = flag.String("base-url", "http://127.0.0.1:8000", "absolute base URL the speaker uses to reach this service (embedded in preset locations)")
		usbPath     = flag.String("usb-config", "/media/sda1/presets.conf", "config file path on the USB stick")
		cachePath   = flag.String("cache", "/mnt/nv/presets.conf", "persistent cache config path")
		mountWait   = flag.Duration("mount-wait", 30*time.Second, "how long to wait for the USB mount at startup")
		reloadEvery = flag.Duration("reload-interval", 15*time.Second, "how often to re-read the config for changes (0 disables)")

		// /full device identity — real values are fetched lazily from infoURL on
		// the first /full request; these are fallbacks if that fetch fails.
		infoURL      = flag.String("info-url", "http://localhost:8090/info", "device info URL for lazy identity (empty disables the fetch)")
		lastFullPath = flag.String("last-full", "/mnt/nv/last-full.xml", "persist the last served /full body here for debugging (empty disables)")
		deviceID     = flag.String("device-id", "AABBCCDDEEFF", "fallback device id for /full")
		firmware     = flag.String("firmware", "27.0.6.46330.5043500", "fallback firmware version for /full")
		serial       = flag.String("serial", "EXAMPLESERIAL0000", "fallback device serial for /full")
		productCode  = flag.String("product-code", "SoundTouch 10", "fallback product code/label for /full")
		deviceName   = flag.String("device-name", "ExampleSpeaker", "fallback device name for /full")

		logRequests = flag.Bool("log-requests", false, "log every HTTP request to syslog (for debugging the boot/preset flow)")
	)
	flag.Parse()

	// Give udev time to mount the USB stick (async, races startup).
	if preset.WaitForFile(*usbPath, *mountWait, 500*time.Millisecond) {
		log.Printf("usb config present at %s", *usbPath)
	} else {
		log.Printf("usb config not found within %s; will try cache %s", *mountWait, *cachePath)
	}

	res, err := preset.Load(*usbPath, *cachePath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	for _, warn := range res.Warnings {
		log.Printf("warning: %s", warn)
	}
	cfg := res.Config
	log.Printf("loaded %d station(s) from %s", len(cfg.Stations), res.Source)

	srv := preset.NewServer(*baseURL, cfg)
	srv.SetIdentity(preset.Identity{
		DeviceID:     *deviceID,
		Name:         *deviceName,
		Serial:       *serial,
		Firmware:     *firmware,
		ProductCode:  *productCode,
		ProductLabel: *productCode,
		IPAddress:    "127.0.0.1",
	}, *infoURL, *lastFullPath)
	srv.SetLogRequests(*logRequests)

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

// reloadLoop periodically applies config changes from the USB stick without a
// restart. It is a thin ticker wrapper around reloadOnce, which holds the
// testable logic.
func reloadLoop(srv *preset.Server, usbPath, cachePath string, every time.Duration, initialFP string) {
	t := time.NewTicker(every)
	defer t.Stop()
	last := initialFP
	for range t.C {
		last, _ = reloadOnce(srv, usbPath, cachePath, last)
	}
}

// reloadOnce performs a single reload check: it loads the config, logs any
// warnings, and swaps the served config only when its fingerprint changed. It
// returns the fingerprint to remember next time and whether a swap happened. On
// load error (e.g. no usb and no cache) the running config is kept unchanged.
func reloadOnce(srv *preset.Server, usbPath, cachePath, last string) (string, bool) {
	res, err := preset.Load(usbPath, cachePath)
	if err != nil {
		return last, false
	}
	for _, warn := range res.Warnings {
		log.Printf("reload warning: %s", warn)
	}
	fp := preset.Fingerprint(res.Config)
	if fp == last {
		return last, false
	}
	srv.SetConfig(res.Config)
	log.Printf("reloaded %d station(s) from %s", len(res.Config.Stations), res.Source)
	return fp, true
}
