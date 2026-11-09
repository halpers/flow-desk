"""Go-issued commands contain identifiers, never model-selected tenant or actor context."""

from typing import Literal
from uuid import UUID

from pydantic import BaseModel, ConfigDict, Field


class ExecutionCommand(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    dispatch_id: UUID
    run_id: UUID
    lease_generation: int = Field(ge=1)
    command: Literal["execute", "resume"]
    graph_version: Literal["read-v1", "ticket-v1", "ticket-v2"]


class CommandReceipt(BaseModel):
    dispatch_id: UUID
    status: str


class CancelCommand(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    dispatch_id: UUID
    run_id: UUID
    lease_generation: int = Field(ge=1)


class IndexCommand(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    job_id: UUID
    version_id: UUID


class CleanupCommand(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    tenant_id: UUID
    version_id: UUID
    job_id: UUID
    content_hash: str = Field(pattern=r"^[0-9a-f]{64}$")


class TrustedContext(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)
    run_id: UUID
    tenant_id: UUID
    actor_user_id: UUID
    lease_generation: int
    capability: str = Field(repr=False)
    content: str
    message_id: UUID
    deadline_at: str
    tools: list[str]
    resume_data: dict = Field(default_factory=dict)


MAX_LIFECYCLE_COUNT = 9_007_199_254_740_991


LifecycleProtectionReason = Literal[
    "COMMAND_QUEUED",
    "COMMAND_RUNNING",
    "COMMAND_WAITING",
    "COMMAND_LEASE_ACTIVE",
    "CALLBACK_UNACKNOWLEDGED",
    "CALLBACK_LEASE_ACTIVE",
    "MODEL_RESERVED",
    "MODEL_STATE_UNKNOWN",
    "EMBEDDING_RESERVED",
    "AGENT_SCOPE_UNAVAILABLE",
]


class AgentLifecycleData(BaseModel):
    """One read-only observation, never a permit to archive or remove runtime data."""

    model_config = ConfigDict(extra="forbid", strict=True, frozen=True)
    available: bool
    observed_at: str = Field(min_length=20, max_length=40)
    observation_only: Literal[True]
    command_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    queued_command_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    running_command_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    waiting_command_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    live_command_lease_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    callback_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    unacknowledged_callback_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    live_callback_lease_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    model_call_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    reserved_model_call_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    checkpoint_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    checkpoint_blob_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    checkpoint_write_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    tool_result_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    execution_span_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    retrieval_attempt_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    embedding_call_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    reserved_embedding_call_count: int = Field(ge=0, le=MAX_LIFECYCLE_COUNT)
    protection_reasons: list[LifecycleProtectionReason] = Field(max_length=10)
