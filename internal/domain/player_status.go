package domain

// PlayerStatus is a bounded, point-in-time view returned by a live game-server
// query. It is deliberately not persisted: names and counts must not become
// stale metadata or be retained longer than the Discord response.
type PlayerStatus struct {
	PlayerCount int
	MaxPlayers  int
	PlayerNames []string
	MissionName string
	MapName     string
}
