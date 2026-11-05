// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package svc

import (
	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/config"

	_ "github.com/lib/pq"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/rest"
	"golang.org/x/time/rate"
)

const loginRequestsPerSecond = 2
const loginBurst = 10

type ServiceContext struct {
	VersionMetadataArchivesModel model.VersionMetadataArchivesModel
	SourceObjectsModel           model.SourceObjectsModel
	RolesModel                   model.RolesModel
	UserRolesModel               model.UserRolesModel
	SourceStore                  client.SourceStore
	TeamsModel                   model.TeamsModel
	TeamMembersModel             model.TeamMembersModel
	RunTicketResourcesModel      model.RunTicketResourcesModel
	ActionsModel                 model.ActionsModel
	ApprovalsModel               model.ApprovalsModel
	PendingInputsModel           model.PendingInputsModel
	TicketsModel                 model.TicketsModel
	TicketNotesModel             model.TicketNotesModel
	TicketEventsModel            model.TicketEventsModel
	ActionReceiptsModel          model.ActionReceiptsModel
	AuditEventsModel             model.AuditEventsModel

	Config                config.Config
	Conn                  sqlx.SqlConn
	UsersModel            model.UsersModel
	TenantsModel          model.TenantsModel
	SessionsModel         model.SessionsModel
	LoginLimiter          *rate.Limiter
	SessionAuth           rest.Middleware
	ServiceAuth           rest.Middleware
	ConversationsModel    model.ConversationsModel
	MessagesModel         model.MessagesModel
	RunsModel             model.RunsModel
	RunEventsModel        model.RunEventsModel
	OutboxEventsModel     model.OutboxEventsModel
	ApiIdempotencyModel   model.ApiIdempotencyModel
	OrdersModel           model.OrdersModel
	LogisticsEventsModel  model.LogisticsEventsModel
	AgentClient           *client.AgentClient
	KnowledgeBasesModel   model.KnowledgeBasesModel
	DocumentsModel        model.DocumentsModel
	DocumentVersionsModel model.DocumentVersionsModel
	RunEvidenceModel      model.RunEvidenceModel
}

func NewServiceContext(c config.Config) *ServiceContext {
	// SQL parameters include credential hashes; never log expanded statements.
	sqlx.DisableLog()
	conn := sqlx.NewSqlConn("postgres", c.DatabaseUrl)
	var sourceStore client.SourceStore
	if c.FileStoreProvider == "s3" || c.FileStoreEndpoint != "" {
		var err error
		sourceStore, err = client.NewS3SourceStore(client.S3SourceConfig{Endpoint: c.FileStoreEndpoint, Region: c.FileStoreRegion, Bucket: c.FileStoreBucket, AccessKey: c.FileStoreAccessKey, SecretKey: c.FileStoreSecretKey, AllowHTTP: c.FileStoreAllowHTTP})
		if err != nil {
			panic("invalid file store configuration")
		}
	}
	if c.FileStoreProvider != "" && c.FileStoreProvider != "postgres" && c.FileStoreProvider != "s3" {
		panic("invalid file store provider")
	}
	return &ServiceContext{
		VersionMetadataArchivesModel: model.NewVersionMetadataArchivesModel(conn),
		SourceObjectsModel:           model.NewSourceObjectsModel(conn),
		RolesModel:                   model.NewRolesModel(conn),
		UserRolesModel:               model.NewUserRolesModel(conn),
		SourceStore:                  sourceStore,
		TeamsModel:                   model.NewTeamsModel(conn),
		TeamMembersModel:             model.NewTeamMembersModel(conn),
		RunTicketResourcesModel:      model.NewRunTicketResourcesModel(conn),
		ActionsModel:                 model.NewActionsModel(conn),
		ApprovalsModel:               model.NewApprovalsModel(conn),
		PendingInputsModel:           model.NewPendingInputsModel(conn),
		TicketsModel:                 model.NewTicketsModel(conn),
		TicketNotesModel:             model.NewTicketNotesModel(conn),
		TicketEventsModel:            model.NewTicketEventsModel(conn),
		ActionReceiptsModel:          model.NewActionReceiptsModel(conn),
		AuditEventsModel:             model.NewAuditEventsModel(conn),

		Config:                c,
		Conn:                  conn,
		UsersModel:            model.NewUsersModel(conn),
		TenantsModel:          model.NewTenantsModel(conn),
		SessionsModel:         model.NewSessionsModel(conn),
		ConversationsModel:    model.NewConversationsModel(conn),
		MessagesModel:         model.NewMessagesModel(conn),
		RunsModel:             model.NewRunsModel(conn),
		RunEventsModel:        model.NewRunEventsModel(conn),
		OutboxEventsModel:     model.NewOutboxEventsModel(conn),
		ApiIdempotencyModel:   model.NewApiIdempotencyModel(conn),
		OrdersModel:           model.NewOrdersModel(conn),
		LogisticsEventsModel:  model.NewLogisticsEventsModel(conn),
		AgentClient:           client.NewAgentClient(c.AgentUrl, c.InternalToken),
		KnowledgeBasesModel:   model.NewKnowledgeBasesModel(conn),
		DocumentsModel:        model.NewDocumentsModel(conn),
		DocumentVersionsModel: model.NewDocumentVersionsModel(conn),
		RunEvidenceModel:      model.NewRunEvidenceModel(conn),
		LoginLimiter:          rate.NewLimiter(rate.Limit(loginRequestsPerSecond), loginBurst),
	}
}
