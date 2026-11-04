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
