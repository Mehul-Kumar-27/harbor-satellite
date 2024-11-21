package config

import (
	"fmt"

	"github.com/robfig/cron/v3"
)

func ValidateCronExpression(job *Job) string {
	_, err := cron.ParseStandard(job.CronExpression)
	if err != nil {
		switch {
		case job.Name == ReplicateStateJobName:
			job.CronExpression = DefaultStateReplicationCron
		case job.Name == UpdateConfigJobName:
			job.CronExpression = DefaultConfigUpdateCron
		default:
			return fmt.Sprintf("Invalid cron job %s expression: %s", job.Name, err.Error())
		}
		return fmt.Sprintf("Invalid cron expression: %s for job %s using default cron schedule %s", err.Error(), job.Name, job.CronExpression)
	}
	return ""
}
