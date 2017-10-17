package ksync

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	apiclient "github.com/docker/docker/client"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

// Service reflects a sync that can be run in the background.
type Service struct {
	Name      string
	Container *Container `structs:"-"`
	image     string     // TODO: make this configurable
	Spec      *Spec      `structs:"-"`
}

// ServiceStatus is the status of a specific service.
type ServiceStatus struct {
	ID        string
	Status    string
	Running   bool
	StartedAt string
}

func (s *ServiceStatus) String() string {
	return YamlString(s)
}

// Fields returns a set of structured fields for logging.
func (s *ServiceStatus) Fields() log.Fields {
	return StructFields(s)
}

// NewService constructs a Service to manage and run local syncs from.
func NewService(name string, cntr *Container, spec *Spec) *Service {
	return &Service{
		Name:      name,
		Container: cntr,
		image:     "busybox",
		Spec:      spec,
	}
}

func (s *Service) String() string {
	return YamlString(s)
}

// Fields returns a set of structured fields for logging.
func (s *Service) Fields() log.Fields {
	return StructFields(s)
}

func (s *Service) containerName() string {
	return fmt.Sprintf("%s-%s", s.Name, s.Container.PodName)
}

// TODO: pull image for users.
// TODO: it is possible for service to not have specs or fully populated
// containers. Make sure to return an error for this use case.
func (s *Service) create() (*container.ContainerCreateCreatedBody, error) {
	cntr, err := dockerClient.ContainerCreate(
		context.Background(),
		&container.Config{
			Cmd:   []string{"/bin/sh", "-c", "while true; do sleep 100; done"},
			Image: "busybox",
			Labels: map[string]string{
				"name":       s.Name,
				"pod":        s.Container.PodName,
				"container":  s.Container.Name,
				"node":       s.Container.NodeName,
				"localPath":  s.Spec.LocalPath,
				"remotePath": s.Spec.RemotePath,
				"heritage":   "ksync",
			},
			// Volumes
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: "on-failure"},
		},
		&network.NetworkingConfig{},
		s.containerName())

	if err != nil {
		if !apiclient.IsErrImageNotFound(err) {
			return nil, ErrorOut("could not create", err, s)
		}

		return nil, fmt.Errorf("run `docker pull %s`", s.image)
	}

	return &cntr, nil
}

// Start runs a service in the background.
func (s *Service) Start() error {
	status, err := s.Status()
	if err != nil {
		return err
	}

	// noop for already running services.
	// TODO: should this return an error? ala. already exists?
	if status.Running {
		return serviceRunningError{
			service: s,
		}
	}

	cntr, err := s.create()
	if err != nil {
		return err
	}

	log.WithFields(MergeFields(s.Fields(), log.Fields{
		"id": cntr.ID,
	})).Debug("container created")

	if err := dockerClient.ContainerStart(
		context.Background(),
		cntr.ID,
		types.ContainerStartOptions{}); err != nil {
		return ErrorOut("could not start", err, s)
	}

	log.WithFields(MergeFields(s.Fields(), log.Fields{
		"id": cntr.ID,
	})).Debug("container started")

	return nil
}

// Stop halts a service that has been running in the background.
func (s *Service) Stop() error {
	status, err := s.Status()
	if err != nil {
		return err
	}

	if !status.Running {
		return fmt.Errorf("must start before you can stop: %s", s.containerName())
	}

	if err := dockerClient.ContainerRemove(
		context.Background(),
		status.ID,
		types.ContainerRemoveOptions{Force: true}); err != nil {
		return errors.Wrap(err, "could not remove")
	}

	log.WithFields(
		MergeFields(s.Fields(), status.Fields())).Debug("container removed")

	return nil
}

// Status checks to see if a service is currently running and looks at its
// status.
func (s *Service) Status() (*ServiceStatus, error) {
	cntr, err := dockerClient.ContainerInspect(
		context.Background(), s.containerName())
	if err != nil {
		if !apiclient.IsErrNotFound(err) {
			return nil, ErrorOut("could not get status", err, s)
		}
		return &ServiceStatus{
			ID:        "",
			Status:    "not created",
			Running:   false,
			StartedAt: "",
		}, nil
	}

	return &ServiceStatus{
		ID:        cntr.ID,
		Status:    cntr.State.Status,
		Running:   cntr.State.Running,
		StartedAt: cntr.State.StartedAt}, nil
}