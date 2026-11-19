// Package eventhub defines internal, typed facts. It grants no remote authority.
package eventhub

const (
	Format               = "pi11-events/1"
	MaxPayloadBytes      = 4096
	MaxEnvelopeBytes     = 8192
	MaxTransactionEvents = 64
	MaxTransactionBytes  = 64 << 10
	MaxBoxStreams        = 64
	MaxStreamRows        = 4096
	MaxStreamBytes       = 4 << 20
	MaxOwnerRows         = 32768
	MaxOwnerBytes        = 32 << 20
	MaxSubscribers       = 64
	MaxStreamSubscribers = 8
	MaxQueueEvents       = 128
	MaxQueueBytes        = 128 << 10
	MaxQueuedBytes       = 2 << 20
	MaxTurnEvents        = 16
	MaxTurnBytes         = 128 << 10
)

type Type string

const (
	TagChanged         Type = "tag.registry.changed"
	ContentChanged     Type = "content.reference.changed"
	CatalogChanged     Type = "catalog.changed"
	CatalogInvalidated Type = "catalog.scope.invalidated"
	AssignmentChanged  Type = "assignment.changed"
	PlacementObserved  Type = "tag.placement.observed"
	RemovalObserved    Type = "tag.removal.observed"
	PlaybackObserved   Type = "playback.observed"
	BoxInvalidated     Type = "box.state.invalidated"
	StorageInvalidated Type = "storage.state.invalidated"
)

type Owner string

const (
	TagRegistry        Owner = "tag-registry"
	ContentStore       Owner = "content-store"
	Catalog            Owner = "catalog"
	Assignment         Owner = "assignment"
	GatewayObservation Owner = "gateway-observation"
	Lifecycle          Owner = "lifecycle"
)

type Stream struct {
	TenantID string  `json:"tenant_id"`
	Kind     string  `json:"kind"`
	BoxID    *string `json:"box_id"`
	Epoch    string  `json:"epoch"`
}
type Source struct {
	Owner      Owner  `json:"owner"`
	InstanceID string `json:"instance_id"`
	Epoch      string `json:"epoch"`
	Sequence   string `json:"sequence"`
}
type Aggregate struct {
	Kind     string  `json:"kind"`
	ID       string  `json:"id"`
	Revision *string `json:"revision"`
}

// Payload is closed to these variants; arbitrary metadata is not an event.
type Payload interface{ eventPayload() }
type TagPayload struct {
	TagID    string `json:"tag_id"`
	Revision string `json:"revision"`
	Change   string `json:"change"`
}

func (TagPayload) eventPayload() {}

type ContentPayload struct {
	ContentID string `json:"content_id"`
	VersionID string `json:"version_id"`
	Change    string `json:"change"`
}

func (ContentPayload) eventPayload() {}

type CatalogPayload struct {
	ContentID string  `json:"content_id"`
	Revision  *string `json:"revision"`
	Change    string  `json:"change"`
}

func (CatalogPayload) eventPayload() {}

type AssignmentPayload struct {
	AssignmentID string `json:"assignment_id"`
	TagID        string `json:"tag_id"`
	Revision     string `json:"revision"`
	Change       string `json:"change"`
}

func (AssignmentPayload) eventPayload() {}

type PlacementPayload struct {
	TagID         *string `json:"tag_id"`
	ProtocolValid string  `json:"protocol_valid"`
}

func (PlacementPayload) eventPayload() {}

type RemovalPayload struct {
	TagID *string `json:"tag_id"`
}

func (RemovalPayload) eventPayload() {}

type PlaybackPayload struct {
	State     string  `json:"state"`
	ContentID *string `json:"content_id"`
	VersionID *string `json:"version_id"`
}

func (PlaybackPayload) eventPayload() {}

type InvalidationPayload struct {
	Reason string `json:"reason"`
}

func (InvalidationPayload) eventPayload() {}

type Envelope struct {
	Format         string    `json:"format"`
	Type           Type      `json:"type"`
	PayloadVersion int       `json:"payload_version"`
	EventID        string    `json:"event_id"`
	Sequence       string    `json:"sequence"`
	Stream         Stream    `json:"stream"`
	Source         Source    `json:"source"`
	Aggregate      Aggregate `json:"aggregate"`
	OccurredAt     *string   `json:"occurred_at"`
	CommittedAt    string    `json:"committed_at"`
	CausationID    *string   `json:"causation_id"`
	CorrelationID  *string   `json:"correlation_id"`
	Payload        Payload   `json:"payload"`
}
