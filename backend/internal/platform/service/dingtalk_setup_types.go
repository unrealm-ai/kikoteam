package service

import (
	"context"
	"time"
)

type DingTalkRegistration struct {
	RetryUntil      *time.Time `json:"retry_until,omitempty"`
	RetryStatus     string     `json:"retry_status,omitempty"`
	ID              string     `json:"id"`
	State           string     `json:"state"`
	AppID           string     `json:"app_id"`
	VerificationURL string     `json:"verification_url,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
	NextPollAt      time.Time  `json:"next_poll_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CredentialSaved bool       `json:"credential_saved"`
	ErrorCode       string     `json:"error_code"`
	WorkspaceID     string     `json:"-"`
	AgentID         string     `json:"-"`
	ActorID         string     `json:"-"`
	OperationKey    string     `json:"-"`
	Version         int64      `json:"-"`
	Interval        int        `json:"poll_interval_seconds"`
	DeviceCipher    []byte     `json:"-"`
	SecretCipher    []byte     `json:"-"`
}
type DingTalkSetupStatus struct {
	Registration *DingTalkRegistration `json:"registration"`
}
type DingTalkBegin struct {
	DeviceCode, URL     string
	ExpiresIn, Interval int
}
type DingTalkPoll struct{ State, AppID, Secret string }
type DingTalkSetupProvider interface {
	Begin(context.Context, string) (DingTalkBegin, error)
	Poll(context.Context, string) (DingTalkPoll, error)
}
type DingTalkSetupTx interface {
	ChannelTx
	Registration(context.Context, string) (DingTalkRegistration, error)
	LatestRegistration(context.Context, string, string) (*DingTalkRegistration, error)
	RegistrationByKey(context.Context, string, string) (*DingTalkRegistration, error)
	InsertRegistration(context.Context, DingTalkRegistration) error
	SaveRegistration(context.Context, DingTalkRegistration) error
}
type DingTalkSetupStore interface {
	WithSetup(context.Context, string, func(DingTalkSetupTx) error) error
}
type DingTalkSetupService struct {
	Store    DingTalkSetupStore
	Channels *ChannelService
	Provider DingTalkSetupProvider
	Source   string
	Now      func() time.Time
}
