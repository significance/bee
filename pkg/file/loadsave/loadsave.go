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
	load file.Loader
	save file.Saver
}

func New(ctx context.Context, storer storage.Storer, mode storage.ModePut, enc bool) file.LoadSaver {
	return &loadSave{
		load: NewLoader(ctx, storer),
		save: NewSaver(ctx, storer, mode, enc),
	}
}

func (ls *loadSave) Load(ref []byte) ([]byte, error) {
	return ls.load.Load(ref)
}

func (ls *loadSave) Save(data []byte) ([]byte, error) {
	return ls.save.Save(data)
}

type load struct {
	ctx    context.Context
	storer storage.Storer
}

func NewLoader(ctx context.Context, storer storage.Storer) file.Loader {
	return &load{
		ctx:    ctx,
		storer: storer,
	}
}

func (l *load) Load(ref []byte) ([]byte, error) {
	ctx := l.ctx

	j, _, err := joiner.New(ctx, l.storer, swarm.NewAddress(ref))
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

type save struct {
	ctx       context.Context
	storer    storage.Storer
	mode      storage.ModePut
	encrypted bool
}

func NewSaver(ctx context.Context, storer storage.Storer, mode storage.ModePut, enc bool) file.Saver {
	return &save{
		ctx:       ctx,
		storer:    storer,
		mode:      mode,
		encrypted: enc,
	}
}

func (s *save) Save(data []byte) ([]byte, error) {
	pipe := builder.NewPipelineBuilder(s.ctx, s.storer, s.mode, s.encrypted)
	address, err := builder.FeedPipeline(s.ctx, pipe, bytes.NewReader(data), int64(len(data)))

	if err != nil {
		return swarm.ZeroAddress.Bytes(), err
	}

	return address.Bytes(), nil

}
