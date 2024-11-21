package config

import (
	"encoding/json"
	"fmt"
	"os"
)

var appConfig *Config

const DefaultConfigPath string = "config.json"
const ReplicateStateJobName string = "replicate_state"
const UpdateConfigJobName string = "update_config"
const DefaultStateReplicationCron string = "@every 00h00m30s"
const DefaultConfigUpdateCron string = "@every 00h00m10s"

type Auth struct {
	Name     string `json:"name,omitempty"`
	Registry string `json:"registry,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

// LocalJsonConfig is a struct that holds the configs that are passed as environment variables
type LocalJsonConfig struct {
	BringOwnRegistry bool   `json:"bring_own_registry"`
	GroundControlURL string `json:"ground_control_url"`
	LogLevel         string `json:"log_level"`
	OwnRegistryAddr  string `json:"own_registry_addr"`
	OwnRegistryPort  string `json:"own_registry_port"`
	UseUnsecure      bool   `json:"use_unsecure"`
	ZotConfigPath    string `json:"zot_config_path"`
	Token            string `json:"token"`
	Jobs             []Job  `json:"jobs"`
}

type StateConfig struct {
	Auth   Auth     `json:"auth,omitempty"`
	States []string `json:"states,omitempty"`
}

type Config struct {
	StateConfig     StateConfig     `json:"state_config"`
	LocalJsonConfig LocalJsonConfig `json:"environment_variables"`
	ZotUrl          string          `json:"zot_url"`
	ConfigPath      string          `json:"config_path"`
}

type Job struct {
	Name           string `json:"name"`
	CronExpression string `json:"cron_expression"`
}

func GetLogLevel() string {
	if appConfig == nil || appConfig.LocalJsonConfig.LogLevel == "" {
		return "info"
	}
	return appConfig.LocalJsonConfig.LogLevel
}

func GetOwnRegistry() bool {
	return appConfig.LocalJsonConfig.BringOwnRegistry
}

func GetOwnRegistryAdr() string {
	return appConfig.LocalJsonConfig.OwnRegistryAddr
}

func GetOwnRegistryPort() string {
	return appConfig.LocalJsonConfig.OwnRegistryPort
}

func GetZotConfigPath() string {
	return appConfig.LocalJsonConfig.ZotConfigPath
}

func GetInput() string {
	return ""
}

func SetZotURL(url string) {
	appConfig.ZotUrl = url
}

func GetZotURL() string {
	return appConfig.ZotUrl
}

func UseUnsecure() bool {
	return appConfig.LocalJsonConfig.UseUnsecure
}

func GetHarborPassword() string {
	return appConfig.StateConfig.Auth.Secret
}

func GetHarborUsername() string {
	return appConfig.StateConfig.Auth.Name
}

func SetRemoteRegistryURL(url string) {
	appConfig.StateConfig.Auth.Registry = url
}

func GetRemoteRegistryURL() string {
	return appConfig.StateConfig.Auth.Registry
}

func GetJobSchedule(jobName string) (string, error) {
	for _, job := range appConfig.LocalJsonConfig.Jobs {
		if job.Name == jobName {
			return job.CronExpression, nil
		}
	}
	return "", fmt.Errorf("job not found: %s", jobName)
}

func GetStates() []string {
	return appConfig.StateConfig.States
}

func GetToken() string {
	return appConfig.LocalJsonConfig.Token
}

func GetGroundControlURL() string {
	return appConfig.LocalJsonConfig.GroundControlURL
}

func SetGroundControlURL(url string) {
	appConfig.LocalJsonConfig.GroundControlURL = url
}

func SetDefaultConfigPath(path string) {
	appConfig.ConfigPath = path
}
func GetDefaultConfigPath() string {
	return appConfig.ConfigPath
}

func ParseConfigFromJson(jsonData string) (*Config, error) {
	var config Config
	err := json.Unmarshal([]byte(jsonData), &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func ReadConfigData(configPath string) ([]byte, error) {

	fileInfo, err := os.Stat(configPath)
	if err != nil {
		return nil, err
	}
	if fileInfo.IsDir() {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func LoadConfig(configPath string) (*Config, []error, []string) {
	var checks []error
	var warnings []string
	var err error
	configData, err := ReadConfigData(configPath)
	if err != nil {
		checks = append(checks, err)
		return nil, checks, warnings
	}
	config, err := ParseConfigFromJson(string(configData))
	if err != nil {
		checks = append(checks, err)
		return nil, checks, warnings
	}
	// Validate the job schedule fields
	for i := range config.LocalJsonConfig.Jobs {
		warning := ValidateCronExpression(&config.LocalJsonConfig.Jobs[i])
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return config, checks, warnings
}

func InitConfig(configPath string) ([]error, []string) {
	var err []error
	var warnings []string
	appConfig, err, warnings = LoadConfig(configPath)
	SetDefaultConfigPath(configPath)
	WriteConfigToPath(configPath)
	return err, warnings
}

func UpdateStateConfig(name, registry, secret string, states []string) {
	appConfig.StateConfig.Auth.Name = name
	appConfig.StateConfig.Auth.Registry = registry
	appConfig.StateConfig.Auth.Secret = secret
	appConfig.StateConfig.States = states
	WriteConfigToPath(appConfig.ConfigPath)
}

func WriteConfigToPath(configPath string) error {
	configData, err := json.MarshalIndent(appConfig, "", "  ")
	if err != nil {
		return err
	}
	err = os.WriteFile(configPath, configData, 0644)
	if err != nil {
		return err
	}
	return nil
}
