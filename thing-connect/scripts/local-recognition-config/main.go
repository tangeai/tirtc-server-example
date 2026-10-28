// Command local-recognition-config prepares a private config for local service expansion.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
	"thing-connect/internal/config"
	"thing-connect/internal/dynamicconfig"
	"thing-connect/internal/recognition"
)

type outputConfig struct {
	Server   config.ServerCfg   `yaml:"server"`
	Log      config.LogCfg      `yaml:"log"`
	Database config.DatabaseCfg `yaml:"database"`
	Redis    config.RedisCfg    `yaml:"redis"`
	JWT      string             `yaml:"jwt_secret"`
	Internal struct {
		Key string `yaml:"key"`
	} `yaml:"internal"`
	Admin struct {
		URL string `yaml:"server_url"`
	} `yaml:"admin"`
	Recognition recognition.Config `yaml:"recognition"`
}

func prepare(source *config.Config) outputConfig {
	var out outputConfig
	out.Server = config.ServerCfg{HTTPPort: 9006, TrustedProxies: source.Server.TrustedProxies}
	out.Log = source.Log
	out.Database = source.Database
	out.Redis = source.Redis
	out.JWT = source.JWTSecret
	out.Internal.Key = source.Internal.Key
	out.Admin.URL = source.Admin.ServerURL
	out.Recognition = recognition.DefaultConfig()
	return out
}
func run(sourcePath, output string) error {
	source, err := config.LoadFile(sourcePath)
	if err != nil {
		return errors.New("无法读取用户服务配置，请检查配置路径及 YAML 字段")
	}
	out := prepare(source)
	data, err := yaml.Marshal(out)
	if err != nil {
		return errors.New("生成识别服务配置失败")
	}
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("无法新建配置输出文件（拒绝覆盖已有文件）")
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("写入识别配置失败")
	}
	return nil
}
func main() {
	source := flag.String("source", "", "user-server config")
	check := flag.Bool("check-mqtt", false, "report ready or pending for recognition MQTT")
	output := flag.String("output", "", "new private output file")
	flag.Parse()
	if *check {
		cfg, err := config.LoadFile(*source)
		if err != nil {
			fmt.Fprintln(os.Stderr, "识别服务配置读取失败")
			os.Exit(1)
		}
		client, err := dynamicconfig.New(cfg.Admin.ServerURL, cfg.Internal.Key, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Admin 连接配置无效")
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_, _, err = dynamicconfig.ResolveMQTT(ctx, client, "recognition-server", cfg.MQTT)
		if errors.Is(err, dynamicconfig.ErrMQTTNotConfigured) {
			fmt.Println("pending")
			return
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "识别服务 MQTT 配置检查失败，请检查 Admin 中的配置")
			os.Exit(1)
		}
		fmt.Println("ready")
		return
	}
	if err := run(*source, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
