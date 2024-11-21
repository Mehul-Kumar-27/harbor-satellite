package satellite

import (
	"context"

	"container-registry.com/harbor-satellite/internal/config"
	"container-registry.com/harbor-satellite/internal/notifier"
	"container-registry.com/harbor-satellite/internal/scheduler"
	"container-registry.com/harbor-satellite/internal/state"
	"container-registry.com/harbor-satellite/internal/utils"
	"container-registry.com/harbor-satellite/logger"
)

type Satellite struct {
	stateReader  state.StateReader
	schedulerKey scheduler.SchedulerKey
}

func NewSatellite(ctx context.Context, schedulerKey scheduler.SchedulerKey) *Satellite {
	return &Satellite{
		schedulerKey: schedulerKey,
	}
}

func (s *Satellite) Run(ctx context.Context) error {
	log := logger.FromContext(ctx)
	log.Info().Msg("Starting Satellite")
	replicateStateJobCron, err := config.GetJobSchedule(config.ReplicateStateJobName)
	if err != nil {
		log.Warn().Msgf("Error in fetching job schedule: %v", err)
		return err
	}
	updateConfigJobCron, err := config.GetJobSchedule(config.UpdateConfigJobName)
	if err != nil {
		log.Warn().Msgf("Error in fetching job schedule: %v", err)
		return err
	}
	userName := config.GetHarborUsername()
	password := config.GetHarborPassword()
	zotURL := config.GetZotURL()
	sourceRegistry := utils.FormatRegistryURL(config.GetRemoteRegistryURL())
	useUnsecure := config.UseUnsecure()
	// Get the scheduler from the context
	scheduler := ctx.Value(s.schedulerKey).(scheduler.Scheduler)
	// Create a simple notifier and add it to the process
	notifier := notifier.NewSimpleNotifier(ctx)
	// Creating a process to fetch and replicate the state
	states := config.GetStates()
	fetchAndReplicateStateProcess := state.NewFetchAndReplicateStateProcess(replicateStateJobCron, notifier, userName, password, zotURL, sourceRegistry, useUnsecure, states)
	configFetchProcess := state.NewFetchConfigFromGroundControlProcess(updateConfigJobCron, "", "")
	err = scheduler.Schedule(configFetchProcess)
	if err != nil {
		log.Error().Err(err).Msg("Error scheduling process")
		return err
	}
	// Add the process to the scheduler
	err = scheduler.Schedule(fetchAndReplicateStateProcess)
	if err != nil {
		log.Error().Err(err).Msg("Error scheduling process")
		return err
	}
	// Schedule Register Satellite Process
	ztrProcess := state.NewZtrProcess(state.DefaultZeroTouchRegistrationCronExpr)
	err = scheduler.Schedule(ztrProcess)
	if err != nil {
		log.Error().Err(err).Msg("Error scheduling process")
		return err
	}
	return nil
}
