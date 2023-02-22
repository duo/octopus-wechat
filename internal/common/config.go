package common

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultRequestTimeout = 1 * time.Minute
	defaultPingInterval   = 30 * time.Second
	//defaultSendTimeout    = 3 * time.Minute
	defaultSyncDelay    = 1 * time.Minute
	defaultSyncInterval = 6 * time.Hour
)

type Configure struct {
	Limb struct {
		Version        string        `yaml:"version"`
		ListenPort     int32         `yaml:"listen_port"`
		HookPort       int32         `yaml:"hook_port"`
		RequestTimeout time.Duration `yaml:"request_timeout"`
	} `yaml:"limb"`

	Service struct {
		Addr         string        `yaml:"addr"`
		Secret       string        `yaml:"secret"`
		PingInterval time.Duration `yaml:"ping_interval"`
		//SendTiemout  time.Duration `yaml:"send_timeout"`
		SyncDelay    time.Duration `yaml:"sync_delay"`
		SyncInterval time.Duration `yaml:"sync_interval"`
	} `yaml:"service"`

	Log struct {
		Level string `yaml:"level"`
	} `yaml:"log"`
}

func LoadConfig(path string) (*Configure, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := &Configure{}
	config.Limb.RequestTimeout = defaultRequestTimeout
	config.Service.PingInterval = defaultPingInterval
	//config.Service.SendTiemout = defaultSendTimeout
	config.Service.SyncDelay = defaultSyncDelay
	config.Service.SyncInterval = defaultSyncInterval
	if err := yaml.Unmarshal(file, &config); err != nil {
		return nil, err
	}

	return config, nil
}
