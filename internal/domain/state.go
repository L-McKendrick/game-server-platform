package domain

// LifecycleState represents the user-visible lifecycle of a session.
type LifecycleState string

const (
	StateDraft         LifecycleState = "DRAFT"
	StateNew           LifecycleState = "NEW"
	StateValidating    LifecycleState = "VALIDATING"
	StateProvisioning  LifecycleState = "PROVISIONING"
	StateBootstrapping LifecycleState = "BOOTSTRAPPING"
	StateInstalling    LifecycleState = "INSTALLING"
	StateReady         LifecycleState = "READY"
	StateRunning       LifecycleState = "RUNNING"
	StateIdle          LifecycleState = "IDLE"
	StateStopping      LifecycleState = "STOPPING"
	StateSleeping      LifecycleState = "SLEEPING"
	StateWaking        LifecycleState = "WAKING"
	StateWarning1      LifecycleState = "WARNING_1"
	StateWarning2      LifecycleState = "WARNING_2"
	StateArchiving     LifecycleState = "ARCHIVING"
	StateDestroying    LifecycleState = "DESTROYING"
	StateArchived      LifecycleState = "ARCHIVED"
	StateRestoring     LifecycleState = "RESTORING"
	StateDeleting      LifecycleState = "DELETING"
	StateDeleted       LifecycleState = "DELETED"
	StateFailed        LifecycleState = "FAILED"
)

var allowedTransitions = map[LifecycleState]map[LifecycleState]struct{}{
	StateDraft: {
		StateNew:      {},
		StateDeleting: {},
	},
	StateNew: {
		StateValidating: {},
		StateDeleting:   {},
	},
	StateValidating: {
		StateProvisioning: {},
		StateFailed:       {},
	},
	StateProvisioning: {
		StateBootstrapping: {},
		StateFailed:        {},
	},
	StateBootstrapping: {
		StateInstalling: {},
		StateFailed:     {},
	},
	StateInstalling: {
		StateReady:  {},
		StateFailed: {},
	},
	StateReady: {
		StateRunning:   {},
		StateStopping:  {},
		StateArchiving: {},
		StateFailed:    {},
	},
	StateRunning: {
		StateIdle:      {},
		StateStopping:  {},
		StateArchiving: {},
		StateFailed:    {},
	},
	StateIdle: {
		StateRunning:   {},
		StateStopping:  {},
		StateArchiving: {},
		StateFailed:    {},
	},
	StateStopping: {
		StateSleeping: {},
		StateFailed:   {},
	},
	StateSleeping: {
		StateWaking:    {},
		StateWarning1:  {},
		StateArchiving: {},
		StateDeleting:  {},
		StateFailed:    {},
	},
	StateWaking: {
		StateRunning: {},
		StateFailed:  {},
	},
	StateWarning1: {
		StateWaking:    {},
		StateWarning2:  {},
		StateArchiving: {},
		StateFailed:    {},
	},
	StateWarning2: {
		StateWaking:    {},
		StateArchiving: {},
		StateFailed:    {},
	},
	StateArchiving: {
		StateDestroying: {},
		StateFailed:     {},
	},
	StateDestroying: {
		StateArchived: {},
		StateFailed:   {},
	},
	StateArchived: {
		StateRestoring: {},
		StateDeleting:  {},
	},
	StateRestoring: {
		StateRunning: {},
		StateFailed:  {},
	},
	StateDeleting: {
		StateDeleted: {},
		StateFailed:  {},
	},
	StateDeleted: {},
	StateFailed: {
		StateValidating:    {},
		StateProvisioning:  {},
		StateBootstrapping: {},
		StateInstalling:    {},
		StateWaking:        {},
		StateArchiving:     {},
		StateRestoring:     {},
		StateDeleting:      {},
	},
}

// Valid reports whether the lifecycle state is recognized.
func (state LifecycleState) Valid() bool {
	_, ok := allowedTransitions[state]
	return ok
}

// CanTransition reports whether a direct transition is permitted.
func (state LifecycleState) CanTransition(to LifecycleState) bool {
	destinations, ok := allowedTransitions[state]
	if !ok {
		return false
	}

	_, ok = destinations[to]
	return ok
}

// HealthStatus represents the observed health of the active workload.
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "UNKNOWN"
	HealthStarting  HealthStatus = "STARTING"
	HealthHealthy   HealthStatus = "HEALTHY"
	HealthDegraded  HealthStatus = "DEGRADED"
	HealthUnhealthy HealthStatus = "UNHEALTHY"
	HealthStopped   HealthStatus = "STOPPED"
)

// Valid reports whether the health value is recognized.
func (status HealthStatus) Valid() bool {
	switch status {
	case HealthUnknown,
		HealthStarting,
		HealthHealthy,
		HealthDegraded,
		HealthUnhealthy,
		HealthStopped:
		return true
	default:
		return false
	}
}
