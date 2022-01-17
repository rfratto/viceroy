package viceroyd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	docker "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/hashicorp/go-multierror"
	"google.golang.org/grpc"
)

// provisioner lazily provisions docker containers.
type provisioner struct {
	log        log.Logger
	opts       Options
	checkImage string

	docker *docker.Client

	mut     sync.Mutex
	clients map[string]*Container
	cb      onProvisioned
}

func newProvisioner(l log.Logger, o Options, cb onProvisioned) (*provisioner, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := docker.NewClientWithOpts(docker.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	imageInfo, _, err := cli.ImageInspectWithRaw(ctx, o.ProvisionerContainer)
	if err != nil {
		level.Warn(l).Log("msg", "could not find reference image", "err", err)
	}

	// TODO(rfratto): GC loop to shut down unused containers

	return &provisioner{
		log:        l,
		opts:       o,
		checkImage: imageInfo.ID,
		docker:     cli,
		clients:    make(map[string]*Container),
		cb:         cb,
	}, nil
}

// onProvisioned is invoked whenever a new container is created.
type onProvisioned func(c *Container) error

func (p *provisioner) GetDefaultContainer(ctx context.Context) (*Container, error) {
	// TODO(rfratto): remove once all code is aware of multiple toolchains
	return p.GetContainer(ctx, p.opts.ProvisionerContainerName)
}

// GetClient returns a client connection to the `name` docker container. If the
// docker container isn't running, it will be created.
func (p *provisioner) GetContainer(ctx context.Context, name string) (*Container, error) {
	p.mut.Lock()
	defer p.mut.Unlock()
	if ent, ok := p.clients[name]; ok {
		return ent, nil
	}

	var (
		container *Container
		err       error
	)

	// Try to see if the container already exists. This might happen when the
	// server restarts.
	cc, err := p.docker.ContainerList(ctx, types.ContainerListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", name)),
	})
	switch {
	case err != nil:
		return nil, fmt.Errorf("searching existing containers: %w", err)
	case len(cc) > 1:
		// Too many existing containers. This should never hit, since we search on
		// the unique container name.
		return nil, fmt.Errorf("unexpected set of %d containers", len(cc))
	case len(cc) == 1:
		// Found an existing container, see if we can use it
		container, err = p.processExistingContainer(ctx, name, cc[0])
	default:
		// No existing container, create a new one
		container, err = p.createNewContainer(ctx, name)
	}

	if err != nil {
		return nil, err
	} else if container == nil {
		return nil, fmt.Errorf("no container created")
	}

	if p.cb != nil {
		if err := p.cb(container); err != nil {
			return nil, fmt.Errorf("provisioner callback for %q failed: %w", name, err)
		}
	}
	p.clients[name] = container
	return container, nil
}

func (p *provisioner) processExistingContainer(ctx context.Context, name string, c types.Container) (*Container, error) {
	info, err := p.docker.ContainerInspect(ctx, c.ID)
	if err != nil {
		return nil, fmt.Errorf("inspecting existing container %q: %w", c.ID, err)
	}

	switch {
	case p.checkImage != "" && info.Image != p.checkImage:
		// The container we found has the wrong image. We can't use it, so we'll
		// forcibly recreate.
		level.Debug(p.log).Log("msg", "existing container has wrong image; recreating", "id", c.ID, "got", info.Image, "expect", p.checkImage)

		if info.State != nil && info.State.Running {
			err := p.docker.ContainerStop(ctx, c.ID, nil)
			if err != nil {
				return nil, fmt.Errorf("stopping existing container: %w", err)
			}
		}
		if !info.HostConfig.AutoRemove {
			err := p.docker.ContainerRemove(ctx, c.ID, types.ContainerRemoveOptions{Force: true})
			if err != nil {
				return nil, fmt.Errorf("removing existing container: %w", err)
			}
		}
		return p.createNewContainer(ctx, name)

	case info.State == nil || !info.State.Running:
		// The container can be used as-is once we start it.
		err := p.docker.ContainerStart(ctx, c.ID, types.ContainerStartOptions{})
		if err != nil {
			return nil, fmt.Errorf("starting existing container: %w", err)
		}
		return p.clientForContainer(ctx, c.ID)

	default:
		return p.clientForContainer(ctx, c.ID)
	}
}

func (p *provisioner) createNewContainer(ctx context.Context, name string) (*Container, error) {
	level.Info(p.log).Log("msg", "provisioning container", "name", name)

	res, err := p.docker.ContainerCreate(ctx, &container.Config{
		ExposedPorts: nat.PortSet{
			nat.Port("8080/tcp"):  struct{}{}, // viceroyworkerd HTTP status
			nat.Port("12194/tcp"): struct{}{}, // viceroyworkerd gRPC
			nat.Port("13613/tcp"): struct{}{}, // viceroyfs gRPC
		},
		Image: p.opts.ProvisionerContainer,
		// TODO(rfratto): customizeable log level?
		Env: []string{"LOG_LEVEL=debug"},
		// TODO(rfratto): labels to indicate managed?
	}, &container.HostConfig{
		PortBindings: nat.PortMap{
			nat.Port("8080/tcp"):  []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}},
			nat.Port("12194/tcp"): []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}},
			nat.Port("13613/tcp"): []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}},
		},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		Resources: container.Resources{
			Devices: []container.DeviceMapping{{
				PathOnHost:        "/dev/fuse",
				PathInContainer:   "/dev/fuse",
				CgroupPermissions: "rwm",
			}},
		},
		CapAdd: []string{"SYS_ADMIN"},
		// TODO(rfratto): mem limits?
	}, &network.NetworkingConfig{
		// TODO(rfratto): provisioned containers should create their own network to
		// prevent escaping.
	}, nil, name)
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}
	for _, warn := range res.Warnings {
		level.Warn(p.log).Log("msg", "received warning when creating container", "warning", warn)
	}

	err = p.docker.ContainerStart(ctx, res.ID, types.ContainerStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to start provisioned container %q: %w", res.ID, err)
	}
	return p.clientForContainer(ctx, res.ID)
}

func (p *provisioner) clientForContainer(ctx context.Context, id string) (*Container, error) {
	res, err := p.docker.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("inspecting container: %w", err)
	}

	var workerCli, fsCli *grpc.ClientConn

	{
		var addr string
		for cPort, binds := range res.NetworkSettings.Ports {
			if cPort != nat.Port("12194/tcp") || len(binds) == 0 {
				continue
			}
			addr = fmt.Sprintf("%s:%s", binds[0].HostIP, binds[0].HostPort)
		}
		if addr == "" {
			return nil, fmt.Errorf("no exposed viceroyworkerd port discovered")
		}

		workerCli, err = grpc.DialContext(ctx, addr, grpc.WithInsecure())
		if err != nil {
			return nil, fmt.Errorf("creating viceroyworkerd connection: %w", err)
		}
	}

	{
		var addr string
		for cPort, binds := range res.NetworkSettings.Ports {
			if cPort != nat.Port("13613/tcp") || len(binds) == 0 {
				continue
			}
			addr = fmt.Sprintf("%s:%s", binds[0].HostIP, binds[0].HostPort)
		}
		if addr == "" {
			return nil, fmt.Errorf("no exposed viceroyfs port discovered")
		}

		fsCli, err = grpc.DialContext(ctx, addr, grpc.WithInsecure())
		if err != nil {
			return nil, fmt.Errorf("creating viceroyfs connection: %w", err)
		}
	}

	return &Container{
		ID:     id,
		Docker: p.docker,

		FS:     fsCli,
		Daemon: workerCli,
	}, nil
}

// Container is a provisioned container.
type Container struct {
	ID     string
	Docker *docker.Client

	// FS and worker daemon connections
	FS, Daemon *grpc.ClientConn
}

func closeContainer(c *Container) error {
	ctx := context.Background()
	errs := multierror.Append(
		c.Daemon.Close(),
		c.FS.Close(),
		c.Docker.ContainerStop(ctx, c.ID, nil),
	)
	return errs.ErrorOrNil()
}

// Close stops the provisioner and all running containers.
func (p *provisioner) Close() error {
	p.mut.Lock()
	defer p.mut.Unlock()

	var errs *multierror.Error
	for _, running := range p.clients {
		err := closeContainer(running)
		if err != nil {
			level.Error(p.log).Log("msg", "failed to close container", "err", err)
			errs = multierror.Append(errs, err)
		}
	}

	p.clients = make(map[string]*Container)
	return errs.ErrorOrNil()
}
