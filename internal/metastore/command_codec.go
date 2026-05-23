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

func MarshalShardSnapshot(snapshot *metastorev1.ShardSnapshot) ([]byte, error) {
	if err := validateShardSnapshot(snapshot); err != nil {
		return nil, err
	}
	return protoMarshal.Marshal(snapshot)
}

func UnmarshalShardSnapshot(data []byte) (*metastorev1.ShardSnapshot, error) {
	var snapshot metastorev1.ShardSnapshot
	if err := proto.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if err := validateShardSnapshot(&snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
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

func validateShardSnapshot(snapshot *metastorev1.ShardSnapshot) error {
	if snapshot == nil {
		return invalidRecord("shard snapshot", "snapshot is required")
	}
	if err := validateSchemaVersion("shard snapshot", snapshot.GetSchemaVersion()); err != nil {
		return err
	}
	if snapshot.GetShardId() == "" {
		return invalidRecord("shard snapshot", "shard_id is required")
	}
	if snapshot.GetMembership() == nil || len(snapshot.GetMembership().GetMembers()) == 0 {
		return invalidRecord("shard snapshot", "membership members are required")
	}
	for i, member := range snapshot.GetMembership().GetMembers() {
		if member.GetRaftId() == 0 {
			return invalidRecord("shard snapshot membership", "member %d raft_id is required", i)
		}
		if member.GetMemberId() == "" {
			return invalidRecord("shard snapshot membership", "member %d member_id is required", i)
		}
		switch member.GetRole() {
		case metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER,
			metastorev1.MembershipRole_MEMBERSHIP_ROLE_LEARNER:
		default:
			return invalidRecord("shard snapshot membership", "member %d role is required", i)
		}
	}
	for i, document := range snapshot.GetDocuments() {
		if err := validateDocumentRecord(document); err != nil {
			return invalidRecord("shard snapshot", "document %d: %v", i, err)
		}
	}
	for i, transaction := range snapshot.GetTransactions() {
		if _, err := marshalTransactionRecord(transaction); err != nil {
			return invalidRecord("shard snapshot", "transaction %d: %v", i, err)
		}
	}
	for i, intent := range snapshot.GetUploadIntents() {
		if _, err := marshalUploadIntentRecord(intent); err != nil {
			return invalidRecord("shard snapshot", "upload intent %d: %v", i, err)
		}
	}
	for i, state := range snapshot.GetRepairStates() {
		if _, err := marshalRepairStateRecord(state); err != nil {
			return invalidRecord("shard snapshot", "repair state %d: %v", i, err)
		}
	}
	for i, receipt := range snapshot.GetCommandReceipts() {
		if _, err := marshalCommandReceiptRecord(receipt); err != nil {
			return invalidRecord("shard snapshot", "command receipt %d: %v", i, err)
		}
	}
	return nil
}
