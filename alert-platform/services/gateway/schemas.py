from pydantic import BaseModel, Field


class RawIngestRequest(BaseModel):
    source_system: str = Field(min_length=1, max_length=64)
    source_instance: str = Field(min_length=1, max_length=128)
    raw_body: str = Field(min_length=1)
    external_id: str | None = None


class IngestAck(BaseModel):
    signal_id: int
    status: str  # queued | duplicate


class HealthResponse(BaseModel):
    status: str
    db: str
    queue_depth: int | None = None
