package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/duo/octopus-wechat/internal/common"
	"github.com/duo/octopus-wechat/internal/limb"

	log "github.com/sirupsen/logrus"
)

func main() {
	config, err := common.LoadConfig("configure.yaml")
	if err != nil {
		log.Fatal(err)
	}

	logLevel, err := log.ParseLevel(config.Log.Level)
	if err == nil {
		log.SetLevel(logLevel)
	}
	log.SetFormatter(&log.TextFormatter{TimestampFormat: "2006-01-02 15:04:05", FullTimestamp: true})

	driver := limb.LoadDriver()
	defer syscall.FreeLibrary(driver)

	limb := limb.NewLimbService(config)
	go limb.Start()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	fmt.Printf("\n")

	limb.Stop()
}
