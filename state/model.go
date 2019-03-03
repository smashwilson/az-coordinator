package state

type DockerImage struct {
	ID     *string `json:"id"`
	Name   string  `json:"name"`
	Tag    string  `json:"tag"`
	Digest string  `json:"digest"`
}

const (
	TypeSimple  = iota
	TypeOneShot = iota
)

type SystemdUnit struct {
	Path    string            `json:"path"`
	Type    int               `json:"type"`
	Secrets []string          `json:"secrets"`
	Env     map[string]string `json:"env"`
}

type State struct {
	Images []DockerImage `json:"images"`
	Units  []SystemdUnit `json:"units"`
}

type Delta struct {
	ImagesToPull   []DockerImage `json:"images_to_pull"`
	ImagesToRemove []DockerImage `json:"images_to_remove"`
	UnitsToCreate  []SystemdUnit `json:"units_to_create"`
	UnitsToModify  []SystemdUnit `json:"units_to_modify"`
	UnitsToDelete  []SystemdUnit `json:"units_to_delete"`
}

func ReadFromSystem() (State, error) {
	return State{}, nil
}

func ReadFromDatabase() (State, error) {
	return State{}, nil
}

func (state State) DeltaFrom(other State) Delta {
	return Delta{}
}

func (delta Delta) Apply() error {
	return nil
}
