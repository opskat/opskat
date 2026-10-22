package kerberos

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/jcmturner/gokrb5/v8/config"
	"github.com/jcmturner/gokrb5/v8/credentials"
	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/iana/errorcode"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/iana/nametype"
	"github.com/jcmturner/gokrb5/v8/iana/patype"
	"github.com/jcmturner/gokrb5/v8/keytab"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/types"
)

// defaultKDCPort 是 KDC 地址没写端口时用的端口。
const defaultKDCPort = "88"

// krbConfig 只提供报文默认值（票据有效期、加密类型偏好、不带客户端地址），不读本机 krb5.conf；
// 构造报文时只读不写，可以共用。
var krbConfig = config.New()

// client 是解析好的一份设置，负责与 KDC 的 AS / TGS 交换。
type client struct {
	principal  string
	cname      types.PrincipalName
	realm      string
	password   string
	keytabFile string
	kdcs       []string // host:port
}

func newClient(s Settings) (*client, error) {
	at := strings.LastIndex(s.Principal, "@")
	if at <= 0 || at == len(s.Principal)-1 {
		return nil, fmt.Errorf("kerberos: principal %q must be user@REALM", s.Principal)
	}
	if len(s.KDCs) == 0 {
		return nil, errors.New("kerberos: no KDC address configured")
	}
	if s.Password == "" && s.KeytabFile == "" {
		return nil, errors.New("kerberos: settings need a password or a keytab file")
	}
	kdcs := make([]string, len(s.KDCs))
	for i, k := range s.KDCs {
		if _, _, err := net.SplitHostPort(k); err != nil {
			k = net.JoinHostPort(k, defaultKDCPort)
		}
		kdcs[i] = k
	}
	return &client{
		principal:  s.Principal,
		cname:      types.NewPrincipalName(nametype.KRB_NT_PRINCIPAL, s.Principal[:at]),
		realm:      s.Principal[at+1:],
		password:   s.Password,
		keytabFile: s.KeytabFile,
		kdcs:       kdcs,
	}, nil
}

// serviceName 解析驱动要的服务主体 service/host[@REALM]；只支持主体所在 realm 的服务（不做跨域）。
func (cl *client) serviceName(spn string) (types.PrincipalName, error) {
	name, realm, hasRealm := strings.Cut(spn, "@")
	if name == "" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return types.PrincipalName{}, fmt.Errorf("kerberos: service principal %q must be service/host", spn)
	}
	if hasRealm && realm != cl.realm {
		return types.PrincipalName{}, fmt.Errorf("kerberos: service principal %q is in realm %q, but principal %s is in realm %q; cross-realm tickets are not supported", spn, realm, cl.principal, cl.realm)
	}
	return types.NewPrincipalName(nametype.KRB_NT_PRINCIPAL, name), nil
}

// credentials 是取 TGT 用的凭据与 AS-REQ 里列出的加密类型：填了 keytab 就读 keytab（每次取 TGT 都重读），
// 只列 keytab 里有该主体密钥的加密类型；否则用口令。
func (cl *client) credentials() (*credentials.Credentials, []int32, error) {
	creds := credentials.NewFromPrincipalName(cl.cname, cl.realm)
	if cl.keytabFile == "" {
		return creds.WithPassword(cl.password), krbConfig.LibDefaults.DefaultTktEnctypeIDs, nil
	}
	b, err := os.ReadFile(cl.keytabFile)
	if err != nil {
		return nil, nil, fmt.Errorf("kerberos: cannot read keytab file: %w", err)
	}
	kt := keytab.New()
	// 解析错误里会带出文件内容，不往外传
	if err := kt.Unmarshal(b); err != nil {
		return nil, nil, fmt.Errorf("kerberos: keytab file %q is not a valid keytab", cl.keytabFile)
	}
	var etypes []int32
	for _, et := range krbConfig.LibDefaults.DefaultTktEnctypeIDs {
		if _, _, err := kt.GetEncryptionKey(cl.cname, cl.realm, 0, et); err == nil {
			etypes = append(etypes, et)
		}
	}
	if len(etypes) == 0 {
		return nil, nil, fmt.Errorf("kerberos: keytab file %q has no key for principal %s", cl.keytabFile, cl.principal)
	}
	return creds.WithKeytab(kt), etypes, nil
}

// asExchange 取 TGT：先不带预认证发 AS-REQ，KDC 要求预认证时按它给的加密类型与盐补上
// PA-ENC-TIMESTAMP 重发。返回 TGT 与应答它的 KDC。
func (cl *client) asExchange(ctx context.Context, dial DialFunc) (*credential, string, error) {
	creds, etypes, err := cl.credentials()
	if err != nil {
		return nil, "", err
	}
	req, err := messages.NewASReqForTGT(cl.realm, krbConfig, cl.cname)
	if err != nil {
		return nil, "", fmt.Errorf("kerberos: build AS-REQ: %w", err)
	}
	req.ReqBody.EType = etypes
	what := "the TGT request for " + cl.principal
	rb, kdc, err := cl.sendAS(ctx, dial, req, what)
	var kdcErr *kdcError
	if errors.As(err, &kdcErr) && kdcErr.krb.ErrorCode == errorcode.KDC_ERR_PREAUTH_REQUIRED {
		pa, perr := cl.encTimestamp(creds, kdcErr.krb)
		if perr != nil {
			return nil, "", perr
		}
		req.PAData = append(req.PAData, pa)
		rb, kdc, err = cl.sendAS(ctx, dial, req, what)
	}
	if err != nil {
		return nil, "", err
	}
	var rep messages.ASRep
	if err := rep.Unmarshal(rb); err != nil {
		return nil, "", fmt.Errorf("kerberos: KDC %s sent an unexpected reply to %s: %w", kdc, what, err)
	}
	if _, err := rep.Verify(krbConfig, creds, req); err != nil {
		return nil, "", fmt.Errorf("kerberos: KDC %s sent an invalid reply to %s: %w", kdc, what, err)
	}
	return &credential{ticket: rep.Ticket, key: rep.DecryptedEncPart.Key, expires: rep.DecryptedEncPart.EndTime}, kdc, nil
}

func (cl *client) sendAS(ctx context.Context, dial DialFunc, req messages.ASReq, what string) ([]byte, string, error) {
	b, err := req.Marshal()
	if err != nil {
		return nil, "", fmt.Errorf("kerberos: marshal AS-REQ: %w", err)
	}
	return cl.send(ctx, dial, b, what)
}

// encTimestamp 是预认证数据 PA-ENC-TIMESTAMP：以 KDC 在 KDC_ERR_PREAUTH_REQUIRED 里指定的加密类型
// （与盐）得到的客户端长期密钥加密当前时间。照 gokrb5 client/ASExchange.go setPAData 的做法。
func (cl *client) encTimestamp(creds *credentials.Credentials, krbErr messages.KRBError) (types.PAData, error) {
	var hints types.PADataSequence
	if err := hints.Unmarshal(krbErr.EData); err != nil {
		return types.PAData{}, fmt.Errorf("kerberos: KDC pre-authentication hint is malformed: %w", err)
	}
	etypeID, err := preauthEType(hints)
	if err != nil {
		return types.PAData{}, err
	}
	var key types.EncryptionKey
	kvno := 0
	if creds.HasKeytab() {
		key, kvno, err = creds.Keytab().GetEncryptionKey(cl.cname, cl.realm, 0, etypeID)
		if err != nil {
			return types.PAData{}, fmt.Errorf("kerberos: keytab file %q has no key of encryption type %d for principal %s, which the KDC requires", cl.keytabFile, etypeID, cl.principal)
		}
	} else {
		key, _, err = crypto.GetKeyFromPassword(creds.Password(), cl.cname, cl.realm, etypeID, hints)
		if err != nil {
			return types.PAData{}, fmt.Errorf("kerberos: derive key from password: %w", err)
		}
	}
	ts, err := types.GetPAEncTSEncAsnMarshalled()
	if err != nil {
		return types.PAData{}, fmt.Errorf("kerberos: build pre-authentication timestamp: %w", err)
	}
	enc, err := crypto.GetEncryptedData(ts, key, keyusage.AS_REQ_PA_ENC_TIMESTAMP, kvno)
	if err != nil {
		return types.PAData{}, fmt.Errorf("kerberos: encrypt pre-authentication timestamp: %w", err)
	}
	b, err := enc.Marshal()
	if err != nil {
		return types.PAData{}, fmt.Errorf("kerberos: marshal pre-authentication timestamp: %w", err)
	}
	return types.PAData{PADataType: patype.PA_ENC_TIMESTAMP, PADataValue: b}, nil
}

// preauthEType 按 RFC 4120 5.2.7.5 取 KDC 指定的预认证加密类型：ETYPE-INFO2 优先于 ETYPE-INFO。
func preauthEType(hints types.PADataSequence) (int32, error) {
	var fromInfo int32
	for _, pa := range hints {
		switch pa.PADataType {
		case patype.PA_ETYPE_INFO2:
			info, err := pa.GetETypeInfo2()
			if err != nil || len(info) == 0 {
				return 0, errors.New("kerberos: KDC pre-authentication hint ETYPE-INFO2 is malformed")
			}
			return info[0].EType, nil
		case patype.PA_ETYPE_INFO:
			info, err := pa.GetETypeInfo()
			if err != nil || len(info) == 0 {
				return 0, errors.New("kerberos: KDC pre-authentication hint ETYPE-INFO is malformed")
			}
			fromInfo = info[0].EType
		}
	}
	if fromInfo == 0 {
		return 0, errors.New("kerberos: KDC requires pre-authentication but names no encryption type")
	}
	return fromInfo, nil
}

// tgsExchange 用 TGT 取 sname 的服务票据，返回服务票据与应答它的 KDC。
func (cl *client) tgsExchange(ctx context.Context, dial DialFunc, tgt *credential, sname types.PrincipalName) (*credential, string, error) {
	req, err := messages.NewTGSReq(cl.cname, cl.realm, krbConfig, tgt.ticket, tgt.key, sname, false)
	if err != nil {
		return nil, "", fmt.Errorf("kerberos: build TGS-REQ: %w", err)
	}
	b, err := req.Marshal()
	if err != nil {
		return nil, "", fmt.Errorf("kerberos: marshal TGS-REQ: %w", err)
	}
	what := fmt.Sprintf("the service ticket request for %s (principal %s)", sname.PrincipalNameString(), cl.principal)
	rb, kdc, err := cl.send(ctx, dial, b, what)
	if err != nil {
		return nil, "", err
	}
	var rep messages.TGSRep
	if err := rep.Unmarshal(rb); err != nil {
		return nil, "", fmt.Errorf("kerberos: KDC %s sent an unexpected reply to %s: %w", kdc, what, err)
	}
	if err := rep.DecryptEncPart(tgt.key); err != nil {
		return nil, "", fmt.Errorf("kerberos: KDC %s sent an undecryptable reply to %s: %w", kdc, what, err)
	}
	if _, err := rep.Verify(krbConfig, req); err != nil {
		return nil, "", fmt.Errorf("kerberos: KDC %s sent an invalid reply to %s: %w", kdc, what, err)
	}
	if !rep.Ticket.SName.Equal(sname) {
		return nil, "", fmt.Errorf("kerberos: KDC %s answered %s with a ticket for %s; cross-realm referrals are not supported", kdc, what, rep.Ticket.SName.PrincipalNameString())
	}
	return &credential{ticket: rep.Ticket, key: rep.DecryptedEncPart.Key, expires: rep.DecryptedEncPart.EndTime}, kdc, nil
}

// apReq 组出交给驱动的登录票据：标准 KRB_AP_REQ——服务票据，加上以服务会话密钥加密、含客户端主体
// 与当前时间的认证符（每次新生成，数据库的重放检查才过得去）。数据库要的形状若不同（GSS-API 包装、
// 校验和、子密钥、双向认证标志），只改这里。
func (cl *client) apReq(svc *credential) ([]byte, error) {
	auth, err := types.NewAuthenticator(cl.realm, cl.cname)
	if err != nil {
		return nil, fmt.Errorf("kerberos: build authenticator: %w", err)
	}
	req, err := messages.NewAPReq(svc.ticket, svc.key, auth)
	if err != nil {
		return nil, fmt.Errorf("kerberos: build AP-REQ: %w", err)
	}
	b, err := req.Marshal()
	if err != nil {
		return nil, fmt.Errorf("kerberos: marshal AP-REQ: %w", err)
	}
	return b, nil
}

// kdcError 是 KDC 回的 KRB-ERROR：给出 KDC、请求、错误名与可读原因。
type kdcError struct {
	kdc  string
	what string
	krb  messages.KRBError
}

func (e *kdcError) Error() string {
	msg := fmt.Sprintf("kerberos: KDC %s rejected %s: %s", e.kdc, e.what, errorcode.Lookup(e.krb.ErrorCode))
	if e.krb.EText != "" {
		msg += " (" + e.krb.EText + ")"
	}
	return msg
}
