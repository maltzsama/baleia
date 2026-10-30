package connections

// S3StorageFields are the optional object-storage fields of an Iceberg REST
// catalog connection. Without them the engine reads table data with the
// credentials the catalog vends (STS); with an access key it uses these
// instead, for stores that have nothing to vend (#511).
func S3StorageFields() []Field {
	return []Field{
		{Key: "storage.endpoint", Label: "S3 endpoint", Type: FieldText, Help: "Only with an S3 access key, e.g. https://<namespace>.compat.objectstorage.<region>.oraclecloud.com; empty uses the AWS default for the region"},
		{Key: "storage.region", Label: "S3 region", Type: FieldText, Help: "Only with an S3 access key, e.g. us-east-1"},
		{Key: "storage.path_style", Label: "Path-style access", Type: FieldBool, Default: "false", Help: "Only with an S3 access key; required by most S3-compatible stores"},
		{Key: "storage.access_key", Label: "S3 access key", Type: FieldText, Help: "Set it when the object store has no STS: the engine then uses these credentials instead of vended ones"},
		{Key: "storage.secret_key", Label: "S3 secret key", Type: FieldSecret},
	}
}
