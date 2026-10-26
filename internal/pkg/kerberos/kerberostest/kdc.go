// Package kerberostest 是测试用的进程内假 KDC：在本机 TCP 端口上按 RFC 4120 的 TCP 分帧收发
// AS / TGS 报文，要求预认证（PA-ENC-TIMESTAMP），能注入 KDC 错误、统计请求次数，并能校验
// 客户端交出的 AP-REQ。只实现 aes256-cts-hmac-sha1-96 一种加密类型，不做跨域与续期。
package kerberostest

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jcmturner/gofork/encoding/asn1"
	"github.com/jcmturner/gokrb5/v8/crypto"
	"github.com/jcmturner/gokrb5/v8/iana"
	"github.com/jcmturner/gokrb5/v8/iana/errorcode"
	"github.com/jcmturner/gokrb5/v8/iana/etypeID"
	"github.com/jcmturner/gokrb5/v8/iana/keyusage"
	"github.com/jcmturner/gokrb5/v8/iana/msgtype"
	"github.com/jcmturner/gokrb5/v8/iana/nametype"
	"github.com/jcmturner/gokrb5/v8/iana/patype"
	"github.com/jcmturner/gokrb5/v8/keytab"
	"github.com/jcmturner/gokrb5/v8/messages"
	"github.com/jcmturner/gokrb5/v8/types"
)

// EType 是假 KDC 唯一使用的加密类型。
const EType = etypeID.AES256_CTS_HMAC_SHA1_96

// KDC 是一个 realm 的假 KDC。
type KDC struct {
	Realm string

	ln net.Listener

	mu        sync.Mutex
	keys      *keytab.Keytab // 所有主体（用户、krbtgt、服务）的长期密钥
	passwords map[string]string
	lifetime  time.Duration
	failWith  int32
	asCount   int
	tgsCount  int
}

// New 在 127.0.0.1 的随机端口上启动 realm 的假 KDC，测试结束时关闭。
func New(t testing.TB, realm string) *KDC {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("kerberostest: listen: %v", err)
	}
	k := &KDC{
		Realm:     realm,
		ln:        ln,
		keys:      keytab.New(),
		passwords: map[string]string{},
		lifetime:  10 * time.Hour,
	}
	k.addKey("krbtgt/"+realm, randomPassword())
	go k.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return k
}

// Addr 是假 KDC 的 host:port。
func (k *KDC) Addr() string { return k.ln.Addr().String() }

// AddUser 登记用户主体 name（不含 @REALM）与口令。
func (k *KDC) AddUser(name, password string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.passwords[name] = password
	k.addKeyLocked(name, password)
}

// AddService 登记服务主体 spn（如 oracle/db.example.com，不含 @REALM）。
func (k *KDC) AddService(spn string) { k.addKey(spn, randomPassword()) }

// SetTicketLifetime 设置之后签发的 TGT 与服务票据的有效期。
func (k *KDC) SetTicketLifetime(d time.Duration) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.lifetime = d
}

// FailWith 让之后的每个请求都回这个 KDC 错误码；0 恢复正常。
func (k *KDC) FailWith(code int32) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.failWith = code
}

// ASRequests 是收到的 AS-REQ 次数（含预认证往返的两次）。
func (k *KDC) ASRequests() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.asCount
}

// TGSRequests 是收到的 TGS-REQ 次数。
func (k *KDC) TGSRequests() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.tgsCount
}

// WriteKeytab 把用户 name 的密钥写成 keytab 文件（放在 t.TempDir() 里），返回路径。
func (k *KDC) WriteKeytab(t testing.TB, name string) string {
	t.Helper()
	k.mu.Lock()
	password, ok := k.passwords[name]
	k.mu.Unlock()
	if !ok {
		t.Fatalf("kerberostest: unknown user %q", name)
	}
	kt := keytab.New()
	if err := kt.AddEntry(name, k.Realm, password, time.Now(), 1, EType); err != nil {
		t.Fatalf("kerberostest: keytab entry: %v", err)
	}
	b, err := kt.Marshal()
	if err != nil {
		t.Fatalf("kerberostest: marshal keytab: %v", err)
	}
	path := filepath.Join(t.TempDir(), strings.ReplaceAll(name, "/", "_")+".keytab")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("kerberostest: write keytab: %v", err)
	}
	return path
}

// APReq 是 VerifyAPReq 从 AP-REQ 里解出的内容。
type APReq struct {
	Client string // 票据与认证符里的客户端主体，user@REALM
	SPN    string // 票据的服务主体（不含 realm）
}

// VerifyAPReq 以服务的长期密钥解开 AP-REQ 的票据，再以票据里的会话密钥解开认证符，
// 两处的客户端主体须一致。
func (k *KDC) VerifyAPReq(b []byte) (APReq, error) {
	var req messages.APReq
	if err := req.Unmarshal(b); err != nil {
		return APReq{}, fmt.Errorf("not a KRB_AP_REQ: %w", err)
	}
	k.mu.Lock()
	err := req.Ticket.DecryptEncPart(k.keys, nil)
	k.mu.Unlock()
	if err != nil {
		return APReq{}, fmt.Errorf("decrypt ticket: %w", err)
	}
	if err := req.DecryptAuthenticator(req.Ticket.DecryptedEncPart.Key); err != nil {
		return APReq{}, err
	}
	if !req.Authenticator.CName.Equal(req.Ticket.DecryptedEncPart.CName) ||
		req.Authenticator.CRealm != req.Ticket.DecryptedEncPart.CRealm {
		return APReq{}, errors.New("authenticator client does not match ticket client")
	}
	return APReq{
		Client: req.Ticket.DecryptedEncPart.CName.PrincipalNameString() + "@" + req.Ticket.DecryptedEncPart.CRealm,
		SPN:    req.Ticket.SName.PrincipalNameString(),
	}, nil
}

func (k *KDC) addKey(name, password string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.addKeyLocked(name, password)
}

func (k *KDC) addKeyLocked(name, password string) {
	if err := k.keys.AddEntry(name, k.Realm, password, time.Now(), 1, EType); err != nil {
		panic(fmt.Sprintf("kerberostest: add key for %s: %v", name, err))
	}
}

func randomPassword() string { return rand.Text() }

func (k *KDC) serve() {
	for {
		conn, err := k.ln.Accept()
		if err != nil {
			return
		}
		go k.handle(conn)
	}
}

// handle 收一个分帧请求、回一个分帧应答，然后关连接（与客户端每次交换一条连接一致）。
func (k *KDC) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var size [4]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return
	}
	req := make([]byte, binary.BigEndian.Uint32(size[:]))
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	rep := k.reply(req)
	out := make([]byte, 4, 4+len(rep))
	binary.BigEndian.PutUint32(out, uint32(len(rep)))
	_, _ = conn.Write(append(out, rep...))
}

func (k *KDC) reply(req []byte) []byte {
	k.mu.Lock()
	defer k.mu.Unlock()
	var as messages.ASReq
	if err := as.Unmarshal(req); err == nil {
		k.asCount++
		if k.failWith != 0 {
			return k.krbError(as.ReqBody.CName, k.failWith, nil)
		}
		return k.asReply(as)
	}
	var tgs messages.TGSReq
	if err := tgs.Unmarshal(req); err == nil {
		k.tgsCount++
		if k.failWith != 0 {
			return k.krbError(tgs.ReqBody.CName, k.failWith, nil)
		}
		return k.tgsReply(tgs)
	}
	return k.krbError(types.PrincipalName{}, errorcode.KRB_ERR_GENERIC, nil)
}

func (k *KDC) asReply(req messages.ASReq) []byte {
	cname := req.ReqBody.CName
	clientKey, _, err := k.keys.GetEncryptionKey(cname, k.Realm, 0, EType)
	if err != nil || cname.NameString[0] == "krbtgt" {
		return k.krbError(cname, errorcode.KDC_ERR_C_PRINCIPAL_UNKNOWN, nil)
	}
	etypeInfo, err := asn1.Marshal(types.ETypeInfo2{{EType: EType, Salt: cname.GetSalt(k.Realm)}})
	if err != nil {
		panic(err)
	}
	hint := types.PADataSequence{{PADataType: patype.PA_ETYPE_INFO2, PADataValue: etypeInfo}}
	if !preauthenticated(req, clientKey) {
		code := int32(errorcode.KDC_ERR_PREAUTH_REQUIRED)
		if req.PAData.Contains(patype.PA_ENC_TIMESTAMP) {
			code = errorcode.KDC_ERR_PREAUTH_FAILED
		}
		edata, err := asn1.Marshal(hint)
		if err != nil {
			panic(err)
		}
		return k.krbError(cname, code, edata)
	}
	krbtgt := types.PrincipalName{NameType: nametype.KRB_NT_SRV_INST, NameString: []string{"krbtgt", k.Realm}}
	return k.issue(msgtype.KRB_AS_REP, cname, krbtgt, req.ReqBody.Nonce, clientKey, keyusage.AS_REP_ENCPART, hint)
}

func preauthenticated(req messages.ASReq, clientKey types.EncryptionKey) bool {
	for _, pa := range req.PAData {
		if pa.PADataType != patype.PA_ENC_TIMESTAMP {
			continue
		}
		var ed types.EncryptedData
		if ed.Unmarshal(pa.PADataValue) != nil {
			return false
		}
		b, err := crypto.DecryptEncPart(ed, clientKey, keyusage.AS_REQ_PA_ENC_TIMESTAMP)
		if err != nil {
			return false
		}
		var ts types.PAEncTSEnc
		return ts.Unmarshal(b) == nil
	}
	return false
}

func (k *KDC) tgsReply(req messages.TGSReq) []byte {
	var ap messages.APReq
	found := false
	for _, pa := range req.PAData {
		if pa.PADataType == patype.PA_TGS_REQ {
			found = ap.Unmarshal(pa.PADataValue) == nil
		}
	}
	if !found || ap.Ticket.DecryptEncPart(k.keys, nil) != nil ||
		ap.DecryptAuthenticator(ap.Ticket.DecryptedEncPart.Key) != nil {
		return k.krbError(req.ReqBody.CName, errorcode.KRB_AP_ERR_BAD_INTEGRITY, nil)
	}
	tgt := ap.Ticket.DecryptedEncPart
	if time.Now().After(tgt.EndTime) {
		return k.krbError(tgt.CName, errorcode.KRB_AP_ERR_TKT_EXPIRED, nil)
	}
	sname := req.ReqBody.SName
	if _, _, err := k.keys.GetEncryptionKey(sname, k.Realm, 0, EType); err != nil {
		return k.krbError(tgt.CName, errorcode.KDC_ERR_S_PRINCIPAL_UNKNOWN, nil)
	}
	return k.issue(msgtype.KRB_TGS_REP, tgt.CName, sname, req.ReqBody.Nonce, tgt.Key, keyusage.TGS_REP_ENCPART_SESSION_KEY, nil)
}

// issue 签发 sname 的票据并组成 AS-REP / TGS-REP；回复的加密部分用 replyKey 以 usage 加密。
func (k *KDC) issue(msgType int, cname, sname types.PrincipalName, nonce int, replyKey types.EncryptionKey, usage uint32, padata types.PADataSequence) []byte {
	now := time.Now().UTC()
	end := now.Add(k.lifetime)
	flags := types.NewKrbFlags()
	tkt, sessionKey, err := messages.NewTicket(cname, k.Realm, sname, k.Realm, flags, k.keys, EType, 1, now, now, end, end)
	if err != nil {
		panic(err)
	}
	enc := messages.EncKDCRepPart{
		Key: sessionKey, LastReqs: []messages.LastReq{}, Nonce: nonce, Flags: flags,
		AuthTime: now, StartTime: now, EndTime: end, SRealm: k.Realm, SName: sname,
	}
	encBytes, err := enc.Marshal()
	if err != nil {
		panic(err)
	}
	encPart, err := crypto.GetEncryptedData(encBytes, replyKey, usage, 1)
	if err != nil {
		panic(err)
	}
	fields := messages.KDCRepFields{
		PVNO: iana.PVNO, MsgType: msgType, PAData: padata,
		CRealm: k.Realm, CName: cname, Ticket: tkt, EncPart: encPart,
	}
	var b []byte
	if msgType == msgtype.KRB_AS_REP {
		rep := messages.ASRep{KDCRepFields: fields}
		b, err = rep.Marshal()
	} else {
		rep := messages.TGSRep{KDCRepFields: fields}
		b, err = rep.Marshal()
	}
	if err != nil {
		panic(err)
	}
	return b
}

func (k *KDC) krbError(cname types.PrincipalName, code int32, edata []byte) []byte {
	e := messages.NewKRBError(types.PrincipalName{NameType: nametype.KRB_NT_SRV_INST, NameString: []string{"krbtgt", k.Realm}}, k.Realm, code, "")
	e.CName = cname
	e.CRealm = k.Realm
	e.EData = edata
	b, err := e.Marshal()
	if err != nil {
		panic(err)
	}
	return b
}
