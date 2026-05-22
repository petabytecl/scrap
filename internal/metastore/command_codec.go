package metastore

import (
	"fmt"

	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"google.golang.org/protobuf/proto"
)

func MarshalShardCommand(command *metastorev1.ShardCommand) ([]byte, error) {
	if err := validateSchemaVersion("shard command", command.GetSchemaVersion()); err != nil {
		return nil, err
	}
	if command.GetCommand() == nil {
		return nil, fmt.Errorf("metastore: shard command is required")
	}
	return protoMarshal.Marshal(command)
}

func UnmarshalShardCommand(data []byte) (*metastorev1.ShardCommand, error) {
	var command metastorev1.ShardCommand
	if err := proto.Unmarshal(data, &command); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion("shard command", command.GetSchemaVersion()); err != nil {
		return nil, err
	}
	if command.GetCommand() == nil {
		return nil, fmt.Errorf("metastore: shard command is required")
	}
	return &command, nil
}
