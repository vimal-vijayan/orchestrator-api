package scalr

// Scalr workspace request and response structures
type ScalrWorkspaceRequest struct {
	Data ScalrWorkspaceData `json:"data"`
}

type ScalrWorkspaceData struct {
	Type          string                      `json:"type"`
	Attributes    ScalrWorkspaceAttributes    `json:"attributes"`
	Relationships ScalrWorkspaceRelationships `json:"relationships"`
}

type ScalrWorkspaceAttributes struct {
	Name        string `json:"name"`
	AutoApply   bool   `json:"auto_apply"`
	IacPlatform string `json:"iac_platform"`
}

type ScalrWorkspaceRelationships struct {
	AgentPool   ScalrAgentPoolRelation   `json:"agent_pool,omitempty"`
	Environment ScalrEnvironmentRelation `json:"environment"`
}

type ScalrAgentPoolRelation struct {
	Data ScalrAgentPoolData `json:"data"`
}

type ScalrAgentPoolData struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type ScalrEnvironmentRelation struct {
	Data ScalrEnvironmentData `json:"data"`
}

type ScalrEnvironmentData struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
