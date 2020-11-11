// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stamp

import (
	"github.com/ethersphere/bee/pkg/file/pipeline"
	"github.com/ethersphere/bee/pkg/postage"
)

type stampWriter struct {
	stamper *postage.Stamper
	next    pipeline.ChainWriter
}

// @zelig the postage package has no interfaces whatsoever, forcing us to depend on far more
// concrete implementations when testing. for example in this case, i must use the concrete service, instead of just injecting
// a simple mock.
func NewStampWriter(stamper *postage.Stamper, next pipeline.ChainWriter) pipeline.ChainWriter {

	return &stampWriter{stamper: stamper, next: next}
}

func (w *stampWriter) ChainWrite(p *pipeline.PipeWriteArgs) error {
	return w.next.ChainWrite(p)
}

func (w *stampWriter) Sum() ([]byte, error) {
	return w.next.Sum()
}
