package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"container-registry.com/harbor-satellite/internal/config"
	"container-registry.com/harbor-satellite/internal/notifier"
	"container-registry.com/harbor-satellite/internal/scheduler"
	"container-registry.com/harbor-satellite/internal/utils"
	"container-registry.com/harbor-satellite/logger"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog"
)

const FetchAndReplicateStateProcessName string = "fetch-replicate-state-process"

type FetchAndReplicateAuthConfig struct {
	Username          string
	Password          string
	UseUnsecure       bool
	RemoteRegistryURL string
	SourceRegistry    string
}

type FetchAndReplicateStateProcess struct {
	id          cron.EntryID
	name        string
	cronExpr    string
	isRunning   bool
	stateMap    []StateMap
	notifier    notifier.Notifier
	mu          *sync.Mutex
	authConfig  FetchAndReplicateAuthConfig
	eventBroker *scheduler.EventBroker
}

type StateMap struct {
	url   string
	State StateReader
}

func NewStateMap(url []string) []StateMap {
	var stateMap []StateMap
	for _, u := range url {
		stateMap = append(stateMap, StateMap{url: u, State: nil})
	}
	return stateMap
}

func NewFetchAndReplicateStateProcess(cronExpr string, notifier notifier.Notifier, username, password, remoteRegistryURL, sourceRegistryURL string, useUnsecure bool, states []string) *FetchAndReplicateStateProcess {
	return &FetchAndReplicateStateProcess{
		name:      FetchAndReplicateStateProcessName,
		cronExpr:  cronExpr,
		isRunning: false,
		notifier:  notifier,
		mu:        &sync.Mutex{},
		stateMap:  NewStateMap(states),
		authConfig: FetchAndReplicateAuthConfig{
			Username:          username,
			Password:          password,
			UseUnsecure:       useUnsecure,
			RemoteRegistryURL: remoteRegistryURL,
			SourceRegistry:    sourceRegistryURL,
		},
	}
}

func (f *FetchAndReplicateStateProcess) Execute(ctx context.Context) error {
	defer f.stop()
	log := logger.FromContext(ctx)
	if !f.start() {
		log.Warn().Msgf("Process %s is already running", f.name)
		return nil
	}
	bool, reason := f.CanExecute(ctx)
	if !bool {
		log.Warn().Msgf("Cannot execute process: %s", reason)
		return nil
	}
	log.Info().Msg(reason)

	for i := range f.stateMap {
		log.Info().Msgf("Processing state for %s", f.stateMap[i].url)
		stateFetcher, err := processInput(f.stateMap[i].url, f.authConfig.Username, f.authConfig.Password, log)
		if err != nil {
			log.Error().Err(err).Msg("Error processing input")
			return err
		}
		newStateFetched, err := f.FetchAndProcessState(stateFetcher, log)
		if err != nil {
			log.Error().Err(err).Msg("Error fetching state")
			return err
		}
		log.Info().Msgf("State fetched successfully for %s", f.stateMap[i].url)
		deleteEntity, replicateEntity, newState := f.GetChanges(newStateFetched, log, f.stateMap[i].State)
		f.LogChanges(deleteEntity, replicateEntity, log)
		if err := f.notifier.Notify(); err != nil {
			log.Error().Err(err).Msg("Error sending notification")
		}

		replicator := NewBasicReplicator(f.authConfig.Username, f.authConfig.Password, f.authConfig.RemoteRegistryURL, f.authConfig.SourceRegistry, f.authConfig.UseUnsecure)
		// Delete the entities from the remote registry
		if err := replicator.DeleteReplicationEntity(ctx, deleteEntity); err != nil {
			log.Error().Err(err).Msg("Error deleting entities")
			return err
		}
		// Replicate the entities to the remote registry
		if err := replicator.Replicate(ctx, replicateEntity); err != nil {
			log.Error().Err(err).Msg("Error replicating state")
			return err
		}
		// Update the state directly in the slice
		f.stateMap[i].State = newState
	}
	return nil
}

func (f *FetchAndReplicateStateProcess) GetChanges(newState StateReader, log *zerolog.Logger, oldState StateReader) ([]ArtifactReader, []ArtifactReader, StateReader) {
	log.Info().Msg("Getting changes")
	// Remove artifacts with null tags from the new state
	newState = f.RemoveNullTagArtifacts(newState)

	var entityToDelete []ArtifactReader
	var entityToReplicate []ArtifactReader

	if oldState == nil {
		log.Warn().Msg("Old state is nil")
		return entityToDelete, newState.GetArtifacts(), newState
	}

	// Create maps for quick lookups
	oldArtifactsMap := make(map[string]ArtifactReader)
	for _, oldArtifact := range oldState.GetArtifacts() {
		tag := oldArtifact.GetTags()[0]
		oldArtifactsMap[oldArtifact.GetName()+"|"+tag] = oldArtifact
	}

	// Check new artifacts and update lists
	for _, newArtifact := range newState.GetArtifacts() {
		nameTagKey := newArtifact.GetName() + "|" + newArtifact.GetTags()[0]
		oldArtifact, exists := oldArtifactsMap[nameTagKey]

		if !exists {
			// New artifact doesn't exist in old state, add to replication list
			entityToReplicate = append(entityToReplicate, newArtifact)
		} else if newArtifact.GetDigest() != oldArtifact.GetDigest() {
			// Artifact exists but has changed, add to both lists
			entityToReplicate = append(entityToReplicate, newArtifact)
			entityToDelete = append(entityToDelete, oldArtifact)
		}

		// Remove processed old artifact from map
		delete(oldArtifactsMap, nameTagKey)
	}

	// Remaining artifacts in oldArtifactsMap should be deleted
	for _, oldArtifact := range oldArtifactsMap {
		entityToDelete = append(entityToDelete, oldArtifact)
	}

	return entityToDelete, entityToReplicate, newState
}
func (f *FetchAndReplicateStateProcess) GetID() cron.EntryID {
	return f.id
}

func (f *FetchAndReplicateStateProcess) SetID(id cron.EntryID) {
	f.id = id
}

func (f *FetchAndReplicateStateProcess) GetName() string {
	return f.name
}

func (f *FetchAndReplicateStateProcess) GetCronExpr() string {
	return f.cronExpr
}

func (f *FetchAndReplicateStateProcess) IsRunning() bool {
	return f.isRunning
}

func (f *FetchAndReplicateStateProcess) CanExecute(ctx context.Context) (bool, string) {
	checks := []struct {
		condition bool
		message   string
	}{
		{f.stateMap == nil, "state map is nil"},
		{f.authConfig.RemoteRegistryURL == "", "remote registry URL is empty"},
		{f.authConfig.SourceRegistry == "", "source registry is empty"},
		{f.authConfig.Username == "", "username is empty"},
		{f.authConfig.Password == "", "password is empty"},
	}

	var missingFields []string
	for _, check := range checks {
		if check.condition {
			missingFields = append(missingFields, check.message)
		}
	}

	if len(missingFields) > 0 {
		return false, fmt.Sprintf("missing %s", strings.Join(missingFields, ", "))
	}

	return true, fmt.Sprintf("Process %s can execute: all conditions fulfilled", f.name)
}

func (f *FetchAndReplicateStateProcess) start() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isRunning {
		return false
	}
	f.isRunning = true
	return true
}

func (f *FetchAndReplicateStateProcess) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.isRunning = false
}

func (f *FetchAndReplicateStateProcess) RemoveNullTagArtifacts(state StateReader) StateReader {
	var artifactsWithoutNullTags []ArtifactReader
	for _, artifact := range state.GetArtifacts() {
		if artifact.GetTags() != nil && len(artifact.GetTags()) != 0 {
			artifactsWithoutNullTags = append(artifactsWithoutNullTags, artifact)
		}
	}
	state.SetArtifacts(artifactsWithoutNullTags)
	return state
}

func PrintPrettyJson(info interface{}, log *zerolog.Logger, message string) error {
	log.Warn().Msg("Printing pretty JSON")
	stateJSON, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		log.Error().Err(err).Msg("Error marshalling state to JSON")
		return err
	}
	log.Info().Msgf("%s: %s", message, stateJSON)
	return nil
}

func ProcessState(state *StateReader) (*StateReader, error) {
	for _, artifact := range (*state).GetArtifacts() {
		repo, image, err := utils.GetRepositoryAndImageNameFromArtifact(artifact.GetRepository())
		if err != nil {
			fmt.Printf("Error in getting repository and image name: %v", err)
			return nil, err
		}
		artifact.SetRepository(repo)
		artifact.SetName(image)
	}
	return state, nil
}

func (f *FetchAndReplicateStateProcess) FetchAndProcessState(fetcher StateFetcher, log *zerolog.Logger) (StateReader, error) {
	state := NewState()
	err := fetcher.FetchStateArtifact(&state)
	if err != nil {
		log.Error().Err(err).Msg("Error fetching state artifact")
		return nil, err
	}
	ProcessState(&state)
	return state, nil
}

func (f *FetchAndReplicateStateProcess) LogChanges(deleteEntity, replicateEntity []ArtifactReader, log *zerolog.Logger) {
	log.Warn().Msgf("Total artifacts to delete: %d", len(deleteEntity))
	log.Warn().Msgf("Total artifacts to replicate: %d", len(replicateEntity))
}

func processInput(input, username, password string, log *zerolog.Logger) (StateFetcher, error) {

	if utils.IsValidURL(input) {
		return processURLInput(utils.FormatRegistryURL(input), username, password, log)
	}

	log.Info().Msg("Input is not a valid URL, checking if it is a file path")
	if err := validateFilePath(input, log); err != nil {
		return nil, err
	}

	return processFileInput(input, username, password, log)
}

func validateFilePath(path string, log *zerolog.Logger) error {
	if utils.HasInvalidPathChars(path) {
		log.Error().Msg("Path contains invalid characters")
		return fmt.Errorf("invalid file path: %s", path)
	}
	if err := utils.GetAbsFilePath(path); err != nil {
		log.Error().Err(err).Msg("No file found")
		return fmt.Errorf("no file found: %s", path)
	}
	return nil
}

func processURLInput(input, username, password string, log *zerolog.Logger) (StateFetcher, error) {
	log.Info().Msg("Input is a valid URL")
	config.SetRemoteRegistryURL(input)

	stateArtifactFetcher := NewURLStateFetcher(input, username, password)

	return stateArtifactFetcher, nil
}

func processFileInput(input, username, password string, log *zerolog.Logger) (StateFetcher, error) {
	log.Info().Msg("Input is a valid file path")
	stateArtifactFetcher := NewFileStateFetcher(input, username, password)
	return stateArtifactFetcher, nil
}

func (f *FetchAndReplicateStateProcess) AddEventBroker(eventBroker *scheduler.EventBroker, ctx context.Context) {
	f.eventBroker = eventBroker
	go f.ListenForUpdatedConfig(ctx)
}

func (f *FetchAndReplicateStateProcess) ListenForUpdatedConfig(ctx context.Context) {
	log := logger.FromContext(ctx)
	log.Info().Msg("Listening for updated config from ground control")
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-f.eventBroker.Subscribe(FetchConfigFromGroundControlEventName):
			log.Info().Msg("Received updated config from ground control")
			payload, ok := event.Payload.(GroundControlPayload)
			if !ok {
				log.Error().Msgf("received invalid payload from %s, for process %s", event.Source, FetchConfigFromGroundControlEventName)
				return
			}
			log.Info().Msgf("Received updated config from ground control with states: %v", len(payload.States))
		case event := <-f.eventBroker.Subscribe(ZeroTouchRegistrationEventName):
			log.Info().Msgf("Received %s event with source %s", event.Name, event.Source)
			payload, ok := event.Payload.(ZeroTouchRegistrationEventPayload)
			if !ok {
				log.Error().Msgf("Received invalid payload from %s, for process %s", event.Source, ZeroTouchRegistrationEventName)
				return
			}
			f.UpdateFetchProcessConfigFromZtr(payload.StateConfig.Auth.Name, payload.StateConfig.Auth.Secret, payload.StateConfig.Auth.Registry, payload.StateConfig.States)
		}
	}
}

func (f *FetchAndReplicateStateProcess) UpdateFetchProcessConfigFromZtr(username, password, sourceRegistryURL string, states []string) {
	f.authConfig.Username = username
	f.authConfig.Password = password
	f.authConfig.SourceRegistry = utils.FormatRegistryURL(sourceRegistryURL)

	// The states contain all the states that this satellite needs to track thus we would have to add the new states to the state map
	// also we would have to remove the states that are not in the new states
	var newStates []string
	for _, state := range states {
		found := false
		for _, stateMap := range f.stateMap {
			if stateMap.url == state {
				found = true
				break
			}
		}
		if !found {
			newStates = append(newStates, state)
		}
	}

	// Remove states that are no longer needed
	var updatedStateMap []StateMap
	for _, stateMap := range f.stateMap {
		if contains(states, stateMap.url) {
			updatedStateMap = append(updatedStateMap, stateMap)
		}
	}

	// Add new states
	f.stateMap = append(updatedStateMap, NewStateMap(newStates)...)
}

// contains takes in a slice and checks if the item is in the slice if preset it returns true else false
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
