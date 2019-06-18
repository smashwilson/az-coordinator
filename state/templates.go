package state

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/template"
)

const (
	// TypeSimple units manage a persistent Docker container as a daemon.
	TypeSimple = iota

	// TypeTimer units fire another unit on a schedule.
	TypeTimer = iota

	// TypeOneShot units execute a container and expect it to terminate in an order fashion.
	TypeOneShot = iota

	// TypeSelf is the special unit used to managed the az-coordinator binary itself.
	TypeSelf = iota
)

type resolvedSystemdUnit struct {
	U        DesiredSystemdUnit
	UnitName string
	Env      map[string]string
	Argv0    string
}

const simpleSource = `[Unit]
Description={{ .UnitName }}
After=docker.service
Requires=docker.service

[Service]
ExecStartPre=-/usr/bin/docker kill {{ .U.Container.Name }}
ExecStartPre=-/usr/bin/docker rm {{ .U.Container.Name }}
ExecStart=/usr/bin/docker run \
  --read-only \
  --network local \
{{- range $key, $value := .Env }}
  --env {{ $key }}="{{ $value }}" \
{{- end }}
{{- range $hostPath, $containerPath := .U.Volumes }}
  --volume {{ $hostPath }}:{{ $containerPath }}:ro \
{{- end }}
{{- range $localPort, $externalPort := .U.Ports }}
  --publish {{ $localPort }}:{{ $externalPort }} \
{{- end }}
  --name {{ .U.Container.Name }} \
  {{ .U.Container.ImageName }}:{{ .U.Container.ImageTag }}

[Install]
WantedBy=multi-user.target
`

var simpleTemplate = template.Must(template.New("simple").Parse(simpleSource))

const oneShotSource = `[Unit]
Description={{ .UnitName }}
Requires=docker.service

[Service]
Type=oneshot
ExecStart=/usr/bin/docker run --rm \
  --read-only \
{{- range $key, $value := .Env }}
  --env {{ $key }}="{{ $value }}" \
{{- end }}
{{- range $hostPath, $containerPath := .U.Volumes }}
  --volume {{ $hostPath }}:{{ $containerPath }}:ro \
{{- end }}
{{- range $localPort, $externalPort := .U.Ports }}
  --publish {{ $localPort }}:{{ $externalPort }} \
{{- end }}
  {{ .U.Container.ImageName }}:{{ .U.Container.ImageTag }}
`

var oneShotTemplate = template.Must(template.New("one-shot").Parse(oneShotSource))

const timerSource = `[Unit]
Description={{ .UnitName }}

[Timer]
OnCalendar={{ .U.Schedule }}

[Install]
WantedBy=timers.target
`

var timerTemplate = template.Must(template.New("timer").Parse(timerSource))

const selfSource = `[Unit]
Description=az-coordinator
After=docker.service
Wants=docker.service

[Service]
User=coordinator
{{- range $key, $value := .Env }}
Environment="{{ $key }}={{ $value }}"
{{- end }}
ExecStart={{ .Argv0 }} serve

[Install]
WantedBy=multi-user.target
`

var selfTemplate = template.Must(template.New("self").Parse(selfSource))

var templatesByType = map[int]*template.Template{
	TypeSimple:  simpleTemplate,
	TypeOneShot: oneShotTemplate,
	TypeTimer:   timerTemplate,
	TypeSelf:    selfTemplate,
}

var typesByName = map[string]int{
	"simple":  TypeSimple,
	"oneshot": TypeOneShot,
	"timer":   TypeTimer,
	"self":    TypeSelf,
}

func getTemplate(templateType int) (*template.Template, error) {
	if t, ok := templatesByType[templateType]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("Invalid template type: %d", templateType)
}

func resolveDesiredUnit(unit DesiredSystemdUnit, session *Session) (*resolvedSystemdUnit, []error) {
	fullEnv := make(map[string]string, len(unit.Env)+len(unit.Secrets))
	errs := make([]error, 0)

	for k, v := range unit.Env {
		fullEnv[k] = strings.ReplaceAll(v, "\n", "\\n\\")
	}

	for _, k := range unit.Secrets {
		v, err := session.secrets.GetRequired(k)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		fullEnv[k] = strings.ReplaceAll(v, "\n", "\\n\\")
	}

	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, errs
	}

	unitName := unit.UnitName()
	if len(unit.Container.Name) != 0 {
		unitName = unit.Container.Name
	}

	return &resolvedSystemdUnit{
		U:        unit,
		UnitName: unitName,
		Env:      fullEnv,
		Argv0:    argv0,
	}, errs
}

// GetTypeWithName returns the unit type enum value corresponding with a friendly string name, or
// an error if the type is unrecognized.
func GetTypeWithName(name string) (int, error) {
	if tp, ok := typesByName[name]; ok {
		return tp, nil
	}
	return 0, fmt.Errorf("Unrecognized type name: %s", name)
}

// WriteUnit uses the template requested by a DesiredSystemdUnit to generate the expected contents of a
// unit file.
func (session *Session) WriteUnit(unit DesiredSystemdUnit, out io.Writer) []error {
	errs := make([]error, 0)

	t, err := getTemplate(unit.Type)
	if err != nil {
		errs = append(errs, err)
	}

	r, rErrs := resolveDesiredUnit(unit, session)
	if len(rErrs) > 0 {
		errs = append(errs, rErrs...)
	}

	if len(errs) > 0 {
		return errs
	}

	if err = t.Execute(out, r); err != nil {
		errs = append(errs, err)
	}

	return errs
}
