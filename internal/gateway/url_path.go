package gateway

import (
	"net/url"
	"strings"
)

// Carry both forms until target construction. Decoding an escaped slash must
// not turn part of an identifier into another path segment on the wire.
type routedPath struct {
	path    string
	rawPath string
}

// joinURLPath keeps the decoded and escaped forms in sync. Only literal
// slashes in the escaped paths are separators; a trailing %2F is path data.
func joinURLPath(base, request *url.URL) (path, rawPath string) {
	if base.RawPath == "" && request.RawPath == "" {
		return singleJoiningSlash(base.Path, request.Path), ""
	}
	basePath, requestPath := base.EscapedPath(), request.EscapedPath()
	baseSlash, requestSlash := strings.HasSuffix(basePath, "/"), strings.HasPrefix(requestPath, "/")
	switch {
	case baseSlash && requestSlash:
		return base.Path + request.Path[1:], basePath + requestPath[1:]
	case !baseSlash && !requestSlash:
		return base.Path + "/" + request.Path, basePath + "/" + requestPath
	default:
		return base.Path + request.Path, basePath + requestPath
	}
}

func splitVendorRequestPath(u *url.URL) (string, routedPath, bool) {
	vendor, escaped, ok := splitVendorPath(u.EscapedPath())
	if !ok {
		return "", routedPath{}, false
	}
	vendor, err := url.PathUnescape(vendor)
	if err != nil || strings.Contains(vendor, "/") {
		return "", routedPath{}, false
	}
	path, err := url.PathUnescape(escaped)
	if err != nil {
		return "", routedPath{}, false
	}
	result := routedPath{path: path}
	if escaped != path {
		result.rawPath = escaped
	}
	return vendor, result, true
}

func (r rewriteMatcher) applyPath(path routedPath) routedPath {
	if path.rawPath == "" {
		return routedPath{path: r.Apply(path.path)}
	}
	if to, ok := r.exact[path.path]; ok {
		return routedPath{path: to} // explicit whole-path replacement
	}
	for _, rule := range r.prefixes {
		if !strings.HasPrefix(path.path, rule.from) {
			continue
		}
		tail := strings.TrimPrefix(path.path, rule.from)
		rawTail := escapedSuffix(path.rawPath, len(rule.from))
		if tail != "" && !strings.HasPrefix(tail, "/") {
			tail, rawTail = "/"+tail, "/"+rawTail
		}
		prefix := strings.TrimRight(rule.to, "/")
		prefixURL := url.URL{Path: prefix}
		return routedPath{path: prefix + tail, rawPath: prefixURL.EscapedPath() + rawTail}
	}
	return path
}

// raw is a valid escaped path and n is a byte count in its decoded form.
func escapedSuffix(raw string, n int) string {
	i := 0
	for n > 0 && i < len(raw) {
		if raw[i] == '%' {
			i += 3
		} else {
			i++
		}
		n--
	}
	return raw[i:]
}
