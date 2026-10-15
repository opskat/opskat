package oss_svc

// CopyObjectWith / MoveObjectWith / RemoveObjectsWith / CreateFolderWith / ListBucketsWith / ProbeWith / DownloadPrefixWith 导出供外部测试包(oss_svc_test)做 gomock 单测。
var (
	CopyObjectWith     = copyObjectWith
	MoveObjectWith     = moveObjectWith
	RemoveObjectsWith  = removeObjectsWith
	CreateFolderWith   = createFolderWith
	ListBucketsWith    = listBucketsWith
	ProbeWith          = probeWith
	DownloadPrefixWith = downloadPrefixWith
)
