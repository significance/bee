// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package logging provides the logger interface abstraction
// and implementation for Bee. It uses logrus under the hood.
package logging

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

type SLogger interface {
	Tracef(format string, args ...interface{})
	Trace(args ...interface{})
	Debugf(format string, args ...interface{})
	Debug(args ...interface{})
	Infof(format string, args ...interface{})
	Info(args ...interface{})
	Warningf(format string, args ...interface{})
	Warning(args ...interface{})
	Errorf(format string, args ...interface{})
	Error(args ...interface{})
	WithField(key string, value interface{}) *logrus.Entry
	WithFields(fields logrus.Fields) *logrus.Entry
	WriterLevel(logrus.Level) *io.PipeWriter
	NewEntry() *logrus.Entry
}

type slogger struct {
	*logrus.Logger
	metrics metrics
}

func BeeSane(w io.Writer, level logrus.Level) Logger {
	l := logrus.New()
	l.SetOutput(w)

	file, err := os.OpenFile("/home/bee/sanity.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
	 l.Out = file
	} else {
	 l.Info("Failed to log to file, using default stderr")
	}

	l.SetLevel(level)
	l.Formatter = &logrus.TextFormatter{
		FullTimestamp: true,
	}
	metrics := newMetrics()
	l.AddHook(metrics)
	return &logger{
		Logger:  l,
		metrics: metrics,
	}
}

func (l *logger) BeeSaneEntry() *logrus.Entry {
	return logrus.NewEntry(l.Logger)
}
