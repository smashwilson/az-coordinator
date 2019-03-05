package state

type Delta struct {
	ImagesToPull   []DesiredDockerImage `json:"images_to_pull"`
	ImagesToRemove []ActualDockerImage  `json:"images_to_remove"`
	UnitsToCreate  []DesiredSystemdUnit `json:"units_to_create"`
	UnitsToModify  []DesiredSystemdUnit `json:"units_to_modify"`
	UnitsToDelete  []ActualSystemdUnit  `json:"units_to_delete"`
}

func Between(desired DesiredState, actual ActualState) Delta {
	imagesByName := make(map[string]ActualDockerImage, len(actual.Images))
	for _, image := range actual.Images {
		imagesByName[image.Name] = image
	}

	imagesToPull := make([]DesiredDockerImage, 0)
	imagesToRemove := make([]ActualDockerImage, 0)
	for _, desired := range desired.Images {
		if actual, ok := imagesByName[desired.Name]; ok {
			if !desired.Matches(actual) {
				imagesToRemove = append(imagesToRemove, actual)
				imagesToPull = append(imagesToPull, desired)
			}
			delete(imagesByName, desired.Name)
		} else {
			imagesToPull = append(imagesToPull, desired)
		}
	}
	for _, actual := range imagesByName {
		imagesToRemove = append(imagesToRemove, actual)
	}

	unitsByPath := make(map[string]ActualSystemdUnit, len(actual.Units))
	for _, unit := range actual.Units {
		unitsByPath[unit.Path] = unit
	}

	unitsToCreate := make([]DesiredSystemdUnit, 0)
	unitsToModify := make([]DesiredSystemdUnit, 0)
	unitsToDelete := make([]ActualSystemdUnit, 0)
	for _, desired := range desired.Units {
		if actual, ok := unitsByPath[desired.Path]; ok {
			if !desired.Matches(actual) {
				if desired.Path == actual.Path {
					unitsToModify = append(unitsToModify, desired)
				} else {
					unitsToDelete = append(unitsToDelete, actual)
					unitsToCreate = append(unitsToCreate, desired)
				}
			}
			delete(unitsByPath, desired.Path)
		} else {
			unitsToCreate = append(unitsToCreate, desired)
		}
	}
	for _, actual := range unitsByPath {
		unitsToDelete = append(unitsToDelete, actual)
	}

	return Delta{
		ImagesToPull:   imagesToPull,
		ImagesToRemove: imagesToRemove,
		UnitsToCreate:  unitsToCreate,
		UnitsToModify:  unitsToModify,
		UnitsToDelete:  unitsToDelete,
	}
}

func (d Delta) Apply() error {
	return nil
}
