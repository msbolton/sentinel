package kafka

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestampMessage() proto.Message { return timestamppb.Now() }
