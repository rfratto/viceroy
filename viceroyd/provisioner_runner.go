package viceroyd

import (
	"context"
	"errors"
	"io"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	pb "github.com/rfratto/viceroy/internal/viceroypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// provisionerRunner implements the Provisioner service by managing and
// proxying commands to containers.
type provisionerRunner struct {
	pb.UnimplementedRunnerServer

	log         log.Logger
	provisioner *provisioner
}

func newProvisionerRunner(l log.Logger, p *provisioner) (*provisionerRunner, error) {
	return &provisionerRunner{
		log:         l,
		provisioner: p,
	}, nil
}

func (pc *provisionerRunner) getClient(ctx context.Context) (pb.RunnerClient, error) {
	cc, err := pc.provisioner.GetDefaultContainer(ctx)
	if err != nil {
		level.Error(pc.log).Log("msg", "failed to get provisioned container client", "err", err)
		return nil, status.Error(codes.Internal, "could not get provisioned container")
	}
	return pb.NewRunnerClient(cc.Daemon), nil
}

func (pc *provisionerRunner) CreateCommand(ctx context.Context, req *pb.CommandRequest) (h *pb.CommandHandle, err error) {
	cli, err := pc.getClient(ctx)
	if err != nil {
		return nil, err
	}
	return cli.CreateCommand(ctx, req)
}

func (pc *provisionerRunner) TailCommand(h *pb.CommandHandle, srv pb.Runner_TailCommandServer) error {
	cli, err := pc.getClient(srv.Context())
	if err != nil {
		return err
	}

	tailCli, err := cli.TailCommand(srv.Context(), h)
	if err != nil {
		return nil
	}
	for {
		data, err := tailCli.Recv()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		if err := srv.Send(data); err != nil {
			return err
		}
	}

	return nil
}

func (pc *provisionerRunner) StartCommand(ctx context.Context, h *pb.CommandHandle) (*pb.CommandStatus, error) {
	cli, err := pc.getClient(ctx)
	if err != nil {
		return nil, err
	}
	return cli.StartCommand(ctx, h)
}

func (pc *provisionerRunner) DeleteCommand(ctx context.Context, h *pb.CommandHandle) (*pb.Empty, error) {
	cli, err := pc.getClient(ctx)
	if err != nil {
		return nil, err
	}
	return cli.DeleteCommand(ctx, h)
}
