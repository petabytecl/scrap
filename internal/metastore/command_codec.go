package metastore

import (
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"google.golang.org/protobuf/proto"
)

func MarshalShardCommand(command *metastorev1.ShardCommand) ([]byte, error) {
	if err := validateShardCommand(command); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(command)
}

func UnmarshalShardCommand(data []byte) (*metastorev1.ShardCommand, error) {
	var command metastorev1.ShardCommand
	if err := proto.Unmarshal(data, &command); err != nil {
		return nil, err
	}
	if err := validateShardCommand(&command); err != nil {
		return nil, err
	}
	return &command, nil
}

func validateShardCommand(command *metastorev1.ShardCommand) error {
	if command == nil {
		return invalidRecord("shard command", "command is required")
	}
	if err := validateSchemaVersion("shard command", command.GetSchemaVersion()); err != nil {
		return err
	}
	if command.GetShardId() == "" {
		return invalidRecord("shard command", "shard_id is required")
	}
	if command.GetCommandId() == "" {
		return invalidRecord("shard command", "command_id is required")
	}
	if err := validateTimestamp("shard command", "proposed_at", command.GetProposedAt()); err != nil {
		return err
	}
	if command.GetCommand() == nil {
		return invalidRecord("shard command", "command body is required")
	}
	if commit := command.GetCommitDocument(); commit != nil {
		if err := validateDocumentRecord(commit.GetDocument()); err != nil {
			return err
		}
	}
	return nil
}
