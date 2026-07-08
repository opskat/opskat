package oss_svc

// ListObjectsWith 导出 listObjectsWith 供外部测试包(oss_svc_test)使用,
// 规避 white-box 测试导入 mock_ossclient(其 import oss_svc)造成的 import cycle。
var ListObjectsWith = listObjectsWith

// CopyObjectWith / MoveObjectWith / RemoveObjectsWith / CreateFolderWith 导出供外部测试包(oss_svc_test)做 gomock 单测。
var (
	CopyObjectWith    = copyObjectWith
	MoveObjectWith    = moveObjectWith
	RemoveObjectsWith = removeObjectsWith
	CreateFolderWith  = createFolderWith
)
