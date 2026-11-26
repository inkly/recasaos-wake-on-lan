package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ReCasaOS/CasaOS-Common/external"
	"github.com/ReCasaOS/CasaOS-Common/model"
	"github.com/ReCasaOS/CasaOS-Common/utils/file"
	"github.com/ReCasaOS/CasaOS-Common/utils/logger"
	"github.com/coreos/go-systemd/daemon"
	"github.com/inkly/casaos-wake-on-lan/common"
	"github.com/inkly/casaos-wake-on-lan/pkg/config"
	"github.com/inkly/casaos-wake-on-lan/route"
	"github.com/inkly/casaos-wake-on-lan/service"
	"github.com/inkly/casaos-wake-on-lan/web"
	"go.uber.org/zap"

	util_http "github.com/ReCasaOS/CasaOS-Common/utils/http"
)

var (
	commit = "private build"
	date   = "private build"

	//go:embed api/wake_on_lan/openapi.yaml
	_docYAML string

	//go:embed build/sysroot/etc/casaos/wake-on-lan.conf.sample
	_confSample string
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// parse arguments and initialize
	{
		configFlag := flag.String("c", "", "config file path")
		versionFlag := flag.Bool("v", false, "version")

		flag.Parse()

		if *versionFlag {
			fmt.Printf("v%s\n", common.Version)
			os.Exit(0)
		}

		println("git commit:", commit)
		println("build date:", date)

		config.InitSetup(*configFlag, _confSample)

		logger.LogInit(config.AppInfo.LogPath, config.AppInfo.LogSaveName, config.AppInfo.LogFileExt)
	}

	// the saved devices: a folder for the service alone (MkdirAll does not tighten one that was there)
	if err := os.MkdirAll(config.AppInfo.StatePath, 0o700); err != nil {
		panic(err)
	}
	if err := os.Chmod(config.AppInfo.StatePath, 0o700); err != nil {
		logger.Warn("the state folder's permissions could not be set to 0700", zap.String("path", config.AppInfo.StatePath), zap.Error(err))
	}
	store := service.OpenStore(filepath.Join(config.AppInfo.StatePath, "devices.json"), func(note string) { logger.Error(note) })

	// setup listener
	listener, err := net.Listen("tcp", net.JoinHostPort(common.Localhost, "0"))
	if err != nil {
		panic(err)
	}
	address := "http://" + listener.Addr().String()

	urlFilePath := filepath.Join(config.CommonInfo.RuntimePath, "wake-on-lan.url")
	if err := file.CreateFileAndWriteContent(urlFilePath, address); err != nil {
		logger.Error("error when creating address file", zap.Error(err), zap.String("address", address), zap.String("filepath", urlFilePath))
	}

	mux := &util_http.HandlerMultiplexer{
		HandlerMap: map[string]http.Handler{
			"v1":  route.InitRouter(route.Deps{Store: store, Wake: service.SystemSender.Wake, Page: web.Page}),
			"doc": route.InitDocRouter(_docYAML),
		},
	}

	s := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second, // fix G112: Potential slowloris attack (see https://github.com/securego/gosec)
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.Shutdown(shutdownCtx); err != nil {
			logger.Error("error when shutting down the http server", zap.Error(err))
		}
		if err := os.Remove(urlFilePath); err != nil && !os.IsNotExist(err) {
			logger.Error("error when removing address file", zap.Error(err), zap.String("filepath", urlFilePath))
		}
	}()

	// ready once listening: the gateway may come later, and `casaos module install` waits for
	// the route by itself
	if supported, err := daemon.SdNotify(false, daemon.SdNotifyReady); err != nil {
		logger.Error("Failed to notify systemd that Wake-on-LAN service is ready", zap.Error(err))
	} else if supported {
		logger.Info("Notified systemd that Wake-on-LAN service is ready")
	} else {
		logger.Info("This process is not running as a systemd service.")
	}

	go func() {
		err := register(ctx, registerRoutes(address), sleep, func(err error, attempt int) {
			log := logger.Warn
			if attempt <= 3 {
				log = logger.Error
			}
			log("the routes could not be registered with the gateway, trying again", zap.Int("attempt", attempt), zap.Error(err))
		})
		if err == nil {
			logger.Info("Wake-on-LAN service is registered with the gateway", zap.String("address", address))
		}
	}()

	err = s.Serve(listener) // not using http.serve() to fix G114: Use of net/http serve function that has no support for setting timeouts (see https://github.com/securego/gosec)
	if err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

// registerRoutes asks the gateway to send the module's two routes to address. Finding the
// gateway is part of it: at boot it may not have written its address yet.
func registerRoutes(address string) func() error {
	return func() error {
		gateway, err := external.NewManagementService(config.CommonInfo.RuntimePath)
		if err != nil {
			return err
		}
		for _, path := range []string{route.APIPath, route.DocPath} {
			if err := gateway.CreateRoute(&model.Route{Path: path, Target: address}); err != nil {
				return err
			}
		}
		return nil
	}
}

// register calls try until it succeeds, waiting 1, 2, 4, 8 then 10 seconds between tries,
// for as long as ctx lasts: a gateway that is late is waited for, and the service never
// exits for it. wait returns false when ctx ended during the wait.
func register(ctx context.Context, try func() error, wait func(context.Context, time.Duration) bool, failed func(error, int)) error {
	delay := time.Second
	for attempt := 1; ; attempt++ {
		err := try()
		if err == nil {
			return nil
		}
		failed(err, attempt)
		if !wait(ctx, delay) {
			return ctx.Err()
		}
		delay = min(2*delay, 10*time.Second)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
