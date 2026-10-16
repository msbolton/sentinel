package main

import (
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/runtime"
)

func runtimeDeps() runtime.Deps { return runtime.Deps{Logger: zap.NewNop()} }
