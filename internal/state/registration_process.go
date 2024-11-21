package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"container-registry.com/harbor-satellite/internal/config"
	"container-registry.com/harbor-satellite/internal/scheduler"
	"container-registry.com/harbor-satellite/logger"
	"github.com/robfig/cron/v3"
)

const ZeroTouchRegistrationRoute = "satellites/ztr"
const ZeroTouchRegistrationProcessName = "zero-touch-registration-process"
const DefaultZeroTouchRegistrationCronExpr string = "@every 00h00m05s"
const ZeroTouchRegistrationEventName = "zero-touch-registration-event"

type ZtrProcess struct {
	// ID is the unique GetID of the process
	ID cron.EntryID
	// Name is the name of the process
	Name string
	// isRunning is true if the process is running
	isRunning bool
	// mu is the mutex to protect the process
	mu *sync.Mutex
	// eventBroker is the event broker to subscribe to the events
	eventBroker *scheduler.EventBroker
	// cronExpr is the cron expression for the process
	cronExpr string
}

func NewZtrProcess(cronExpr string) *ZtrProcess {
	return &ZtrProcess{
		Name:     ZeroTouchRegistrationProcessName,
		cronExpr: cronExpr,
		mu:       &sync.Mutex{},
	}
}

type ZeroTouchRegistrationEventPayload struct {
	StateConfig config.StateConfig
}

func (z *ZtrProcess) Execute(ctx context.Context) error {
	log := logger.FromContext(ctx)
	if !z.start() {
		log.Warn().Msgf("Process %s is already running", z.Name)
		return nil
	}
	defer z.stop()
	canExecute, reason := z.CanExecute(ctx)
	if !canExecute {
		log.Warn().Msgf("Process %s cannot execute: %s", z.Name, reason)
		return nil
	}
	log.Info().Msgf("Executing process %s", z.Name)

	// Register the satellite
	err, stateConfig := RegisterSatellite(config.GetGroundControlURL(), ZeroTouchRegistrationRoute, config.GetToken(), ctx)
	if err != nil {
		log.Error().Msgf("Failed to register satellite: %v", err)
		return err
	}
	if stateConfig.Auth.Name == "" || stateConfig.Auth.Secret == "" || stateConfig.Auth.Registry == "" {
		log.Error().Msgf("Failed to register satellite: %v", err)
		return fmt.Errorf("failed to register satellite: %w", err)
	}
	// Update the state config in app config
	config.UpdateStateConfig(stateConfig.Auth.Name, stateConfig.Auth.Registry, stateConfig.Auth.Secret, stateConfig.States)
	zeroTouchRegistrationEvent := scheduler.Event{
		Name: ZeroTouchRegistrationEventName,
		Payload: ZeroTouchRegistrationEventPayload{
			StateConfig: stateConfig,
		},
		Source: ZeroTouchRegistrationProcessName,
	}
	z.eventBroker.Publish(zeroTouchRegistrationEvent, ctx)
	stopProcessPayload := scheduler.StopProcessEventPayload{
		ProcessName: z.GetName(),
		Id:          z.GetID(),
	}
	stopProcessEvent := scheduler.Event{
		Name:    scheduler.StopProcessEventName,
		Payload: stopProcessPayload,
		Source:  ZeroTouchRegistrationProcessName,
	}
	z.eventBroker.Publish(stopProcessEvent, ctx)
	return nil
}

func (z *ZtrProcess) GetID() cron.EntryID {
	return z.ID
}

func (z *ZtrProcess) SetID(id cron.EntryID) {
	z.ID = id
}

func (z *ZtrProcess) GetName() string {
	return z.Name
}

func (z *ZtrProcess) GetCronExpr() string {
	return z.cronExpr
}

func (z *ZtrProcess) IsRunning() bool {
	return z.isRunning
}

// CanExecute checks if the process can execute.
// It returns true if the process can execute, false otherwise.
func (z *ZtrProcess) CanExecute(ctx context.Context) (bool, string) {
	log := logger.FromContext(ctx)
	log.Info().Msgf("Checking if process %s can execute", z.Name)
	errors, warnings := z.loadConfig()
	if len(errors) > 0 || len(warnings) > 0 {
		for _, warning := range warnings {
			log.Warn().Msgf("Warning loading config: %v", warning)
		}
		for _, err := range errors {
			log.Error().Msgf("Error loading config: %v", err)
		}
		return false, "error loading config"
	}

	checks := []struct {
		condition bool
		message   string
	}{
		{config.GetToken() == "", "token"},
		{config.GetGroundControlURL() == "", "ground control URL"},
	}
	var missing []string
	for _, check := range checks {
		if check.condition {
			missing = append(missing, check.message)
		}
	}
	if len(missing) > 0 {
		return false, fmt.Sprintf("missing %s, please update config present at %s", strings.Join(missing, ", "), config.GetDefaultConfigPath())
	}

	return true, fmt.Sprintf("Process %s can execute all conditions fulfilled", z.Name)
}

func (z *ZtrProcess) AddEventBroker(eventBroker *scheduler.EventBroker, context context.Context) {
	z.eventBroker = eventBroker
}

func (z *ZtrProcess) start() bool {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.isRunning {
		return false
	}
	z.isRunning = true
	return true
}

func (z *ZtrProcess) stop() {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.isRunning = false
}

// loadConfig loads the configuration.
// It returns an error if the configuration cannot be loaded.
func (z *ZtrProcess) loadConfig() ([]error, []string) {
	return config.InitConfig(config.GetDefaultConfigPath())
}

func RegisterSatellite(groundControlURL, path, token string, ctx context.Context) (error, config.StateConfig) {
	ztrURL := fmt.Sprintf("%s/%s/%s", groundControlURL, path, token)
	client := &http.Client{}

	// Create a new request for the Zero Touch Registration of satellite
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ztrURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err), config.StateConfig{}
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err), config.StateConfig{}
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to register satellite: %s", response.Status), config.StateConfig{}
	}

	var authResponse config.StateConfig
	if err := json.NewDecoder(response.Body).Decode(&authResponse); err != nil {
		return fmt.Errorf("failed to decode response: %w", err), config.StateConfig{}
	}

	return nil, authResponse
}
