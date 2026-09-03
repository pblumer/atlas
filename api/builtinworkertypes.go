package api

// WorkerTypeOrigin identifies where a Worker Type definition comes from.
type WorkerTypeOrigin string

const (
	// WorkerTypeOriginBuiltIn means the Worker Type metadata ships with this Atlas
	// release and its trusted implementation is part of the Atlas binary.
	WorkerTypeOriginBuiltIn WorkerTypeOrigin = "built-in"
)

// builtInWorkerTypeMetadata is the design-time package metadata Atlas ships for one
// managed integration capability. Runtime wiring, reserved job-type indices and
// connector-store behavior remain owned by managedConnectorKinds; this metadata is
// deliberately not another execution registry.
type builtInWorkerTypeMetadata struct {
	ConnectorKind string
	ID            string
	Version       string
	Title         string
	Vendor        string
	Origin        WorkerTypeOrigin
	RuntimeMode   WorkerRuntimeMode
}

const initialBuiltInWorkerTypeVersion = "1.0.0"

var builtInManagedWorkerTypes = []builtInWorkerTypeMetadata{
	{ConnectorKind: connectorKindTemis, ID: "atlas.temis", Version: initialBuiltInWorkerTypeVersion, Title: "Temis DMN", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasEmbedded},
	{ConnectorKind: connectorKindClio, ID: "atlas.clio", Version: initialBuiltInWorkerTypeVersion, Title: "Clio", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasEmbedded},
	{ConnectorKind: connectorKindMail, ID: "atlas.mail", Version: initialBuiltInWorkerTypeVersion, Title: "Mail", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasEmbedded},
	{ConnectorKind: connectorKindSharePoint, ID: "atlas.sharepoint", Version: initialBuiltInWorkerTypeVersion, Title: "Microsoft SharePoint", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasEmbedded},
	{ConnectorKind: connectorKindRemedy, ID: "atlas.remedy", Version: initialBuiltInWorkerTypeVersion, Title: "BMC Remedy", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasEmbedded},
	{ConnectorKind: connectorKindJira, ID: "atlas.jira", Version: initialBuiltInWorkerTypeVersion, Title: "Atlassian Jira", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasEmbedded},
	{ConnectorKind: connectorKindGoogleSheets, ID: "atlas.googlesheets", Version: initialBuiltInWorkerTypeVersion, Title: "Google Sheets", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasEmbedded},
	{ConnectorKind: connectorKindEntra, ID: "atlas.entra", Version: initialBuiltInWorkerTypeVersion, Title: "Microsoft Entra ID", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasSupervised},
	{ConnectorKind: connectorKindAD, ID: "atlas.ad", Version: initialBuiltInWorkerTypeVersion, Title: "Microsoft Active Directory", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasSupervised},
	{ConnectorKind: connectorKindPostgres, ID: "atlas.postgres", Version: initialBuiltInWorkerTypeVersion, Title: "PostgreSQL", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasSupervised},
	{ConnectorKind: connectorKindMariaDB, ID: "atlas.mariadb", Version: initialBuiltInWorkerTypeVersion, Title: "MariaDB", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasSupervised},
	{ConnectorKind: connectorKindMSSQL, ID: "atlas.mssql", Version: initialBuiltInWorkerTypeVersion, Title: "Microsoft SQL Server", Vendor: "Atlas", Origin: WorkerTypeOriginBuiltIn, RuntimeMode: WorkerRuntimeModeAtlasSupervised},
}

func lookupBuiltInManagedWorkerType(connectorKind string) (builtInWorkerTypeMetadata, bool) {
	for _, meta := range builtInManagedWorkerTypes {
		if meta.ConnectorKind == connectorKind {
			return meta, true
		}
	}
	return builtInWorkerTypeMetadata{}, false
}
