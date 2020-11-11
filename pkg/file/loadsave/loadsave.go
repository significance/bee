package loadsave

import (
	"bytes"
	"context"

	"github.com/ethersphere/bee/pkg/file"
	"github.com/ethersphere/bee/pkg/file/joiner"
	"github.com/ethersphere/bee/pkg/file/pipeline/builder"
	"github.com/ethersphere/bee/pkg/storage"
	"github.com/ethersphere/bee/pkg/swarm"
)

// loadSave is needed for manifest operations and provides
// simple wrapping over load and save operations using file
// package abstractions. use with caution since Loader will
// load all of the subtrie of a given hash in memory.
type loadSave struct {
	ctx       context.Context
	storer    storage.Storer
	mode      storage.ModePut
	encrypted bool
}

func New(ctx context.Context, storer storage.Storer, mode storage.ModePut, enc bool) file.LoadSaver {
	return &loadSave{
		ctx:       ctx,
		storer:    storer,
		mode:      mode,
		encrypted: enc,
	}
}

func (ls *loadSave) Load(ref []byte) ([]byte, error) {
	ctx := ls.ctx

	j, _, err := joiner.New(ctx, ls.storer, swarm.NewAddress(ref))
	if err != nil {
		return nil, err
	}

	buf := bytes.NewBuffer(nil)
	_, err = file.JoinReadAll(ctx, j, buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (ls *loadSave) Save(data []byte) ([]byte, error) {
	pipe := builder.NewPipelineBuilder(ls.ctx, ls.storer, ls.mode, ls.encrypted)
	address, err := builder.FeedPipeline(ls.ctx, pipe, bytes.NewReader(data), int64(len(data)))

	if err != nil {
		return swarm.ZeroAddress.Bytes(), err
	}

	return address.Bytes(), nil

}
