package tui

import "github.com/novusedge/stoat/internal/core"

type paramForm struct{}

func newParamForm(core.Recipe) *paramForm { return &paramForm{} }

func (*paramForm) Values() map[string]string { return map[string]string{} }

func (*paramForm) Complete() bool { return false }
