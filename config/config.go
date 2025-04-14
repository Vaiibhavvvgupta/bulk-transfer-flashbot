package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds application configuration
type Config struct {
	RpcUrl         string `yaml:"rpc_url"`
	FlashbotsRelay string `yaml:"flashbots_relay"`
	AuthKey        string `yaml:"auth_key"`
	UsdtAddress    string `yaml:"usdt_address"`
	Port           string `yaml:"port"`
	Environment    string `yaml:"environment"`
}

// Load reads configuration from a YAML file
func Load(file string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(file)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}