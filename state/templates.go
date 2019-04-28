package state

import (
	"fmt"
	"io"
	"text/template"
)

const (
	TypeSimple  = iota
	TypeTimer   = iota
	TypeOneShot = iota
)

type resolvedSystemdUnit struct {
	U   DesiredSystemdUnit
	Env map[string]string
}

const simpleSource = `[Unit]
Description={{ .Description }}
After=docker.service
Requires=docker.service

[Service]
ExecStartPre=-/usr/bin/docker kill {{ .U.Container.Name }}
ExecStartPre=-/usr/bin/docker rm {{ .U.Container.Name }}
ExecStart=/usr/bin/docker run \
  --read-only \
  --network local \
{{ range $key, $value := .Env }}
  --env {{ $key }}="{{ $value }}" \
{{ end }}
{{ range $hostPath, $containerPath := .U.Volumes }}
  --volume {{ $hostPath }}:{{ $containerPath }}:ro \
{{ end }}
{{ range $localPort, $externalPort := .U.Ports }}
  --publish {{ $localPort }}:{{ $externalPort }} \
{{ end }}
  --name {{ .U.Container.Name }} \
  {{ .U.Container.ImageName }}:{{ .U.Container.ImageTag }}

[Install]
WantedBy=multi-user.target
`

var simpleTemplate = template.Must(template.New("simple").Parse(simpleSource))

const oneShotSource = `[Unit]
Description={{ .U.Description }}
Requires=docker.service

[Service]
Type=oneshot
ExecStart=/usr/bin/docker run --rm \
  --read-only \
{{ range $key, $value := .Env }}
  --env {{ $key }}="{{ $value }}" \
{{ end }}
{{ range $hostPath, $containerPath := .U.Volumes }}
  --volume {{ $hostPath }}:{{ $containerPath }}:ro \
{{ end }}
{{ range $localPort, $externalPort := .U.Ports }}
  --publish {{ $localPort }}:{{ $externalPort }} \
{{ end }}
  {{ .U.Container.ImageName }}:{{ .U.Container.ImageTag }}
`

var oneShotTemplate = template.Must(template.New("one-shot").Parse(oneShotSource))

const timerSource = `[Unit]
Description={{ .U.Description }}

[Timer]
OnCalendar={{ .U.Schedule }}

[Install]
WantedBy=timers.target
`

var timerTemplate = template.Must(template.New("timer").Parse(timerSource))

var templatesByType = map[int]*template.Template{
	TypeSimple:  simpleTemplate,
	TypeOneShot: oneShotTemplate,
	TypeTimer:   timerTemplate,
}

func getTemplate(templateType int) (*template.Template, error) {
	if t, ok := templatesByType[templateType]; ok {
		return t, nil
	} else {
		return nil, fmt.Errorf("Invalid template type: %d", templateType)
	}
}

func resolveDesiredUnit(unit DesiredSystemdUnit, session *Session) (*resolvedSystemdUnit, []error) {
	fullEnv := make(map[string]string, len(unit.Env)+len(unit.Secrets))
	errs := make([]error, 0)

	for k, v := range unit.Env {
		fullEnv[k] = v
	}

	for _, k := range unit.Secrets {
		v, err := session.secrets.GetRequired(k)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		fullEnv[k] = v
	}

	if len(errs) > 0 {
		return nil, errs
	}

	return &resolvedSystemdUnit{
		U:   unit,
		Env: fullEnv,
	}, errs
}

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
