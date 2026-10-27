package common

// ---------------------------------------------------------------------------
// HTTP header 合法性校验: 与 net/http 写出请求时的检查等价, 前移到 Prepare 阶段,
// 让 dry-run 与真实发送判定一致, 否则非法 header 要到 client.Do 才被拒
// ValidHeaderName 防的是空名/非法字符, ValidHeaderValue 防的是 CR/LF 头部注入与请求分裂
//
// HTTP header validity checks, equivalent to what net/http performs while writing a request but
// moved forward into Prepare so a dry-run and a real send judge alike, since client.Do is the
// point that rejects an illegal header otherwise
// ValidHeaderName guards against an empty name and invalid characters, and ValidHeaderValue guards
// against CR/LF header injection and request splitting
// ---------------------------------------------------------------------------

// headerTokenTable 是 RFC 7230 的 tchar 集合: 字母数字与 !#$%&'*+-.^_`|~
// 表按字节索引, 因此 >=0x80 的字节由调用方先挡掉
//
// headerTokenTable is the RFC 7230 tchar set: letters, digits and !#$%&'*+-.^_`|~
// The table is indexed by byte, so a caller rejects a byte >= 0x80 before consulting it
var headerTokenTable = func() [128]bool {
	var t [128]bool
	for c := range t {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			t[c] = true
		}
	}
	for _, c := range []byte("!#$%&'*+-.^_|~") {
		t[c] = true
	}
	t['`'] = true // 反引号同样属于 tchar
	return t
}()

// ValidHeaderName 校验 header 名: 非空且每个字节都是 ASCII tchar
// 空格, 冒号, 斜杠与控制字符都会让 net/http 在写出时报错, 因此这里提前拒绝
//
// ValidHeaderName checks a header name: non-empty and every byte an ASCII tchar
// A space, colon, slash or control character would make net/http fail while writing, so it is
// refused here in advance
func ValidHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 0x80 || !headerTokenTable[b] {
			return false
		}
	}
	return true
}

// ValidHeaderValue 校验 header 值: 禁止 CR/LF/NUL 等控制字符与 DEL, 允许可见 ASCII, SP, HTAB
// 以及非 ASCII (obs-text, 由 net/http 自行处理)
// 空值合法, 因为协议允许空值的 header
//
// ValidHeaderValue checks a header value: control characters such as CR, LF and NUL plus DEL are
// forbidden, while printable ASCII, SP, HTAB and non-ASCII (obs-text, handled by net/http) are
// allowed
// An empty value is valid, since the protocol permits a header with an empty value
func ValidHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 0x80 || b == ' ' || b == '\t' {
			continue
		}
		// 0x00-0x1f 覆盖 NUL/CR/LF 与其余控制字符, 0x7f 是 DEL
		// 0x00-0x1f covers NUL, CR, LF and the other controls, and 0x7f is DEL
		if b <= 0x1f || b == 0x7f {
			return false
		}
	}
	return true
}
