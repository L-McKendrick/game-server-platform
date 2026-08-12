package domain

// HealthObservation contains only safe, bounded host health values.
type HealthObservation struct {
	ArmaService          bool
	ArmaUDP              bool
	TeamSpeakService     bool
	TeamSpeakUDP         bool
	DiskUsedPercent      int
	MemoryAvailableBytes int64
	PlayerCount          int
}

func (observation HealthObservation) Classify(teamSpeakEnabled bool) HealthStatus {
	if !observation.ArmaService || !observation.ArmaUDP {
		return HealthUnhealthy
	}
	if teamSpeakEnabled && (!observation.TeamSpeakService || !observation.TeamSpeakUDP) {
		return HealthDegraded
	}
	if observation.DiskUsedPercent >= 90 {
		return HealthDegraded
	}
	return HealthHealthy
}
