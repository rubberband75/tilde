package worker

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"tilde/internal/protocol"
)

// The session's polkit authentication agent (D44). When a program in the
// web session wants something polkit only allows after a password
// (mounting a drive through udisks, pkexec in a terminal, systemctl), polkit
// asks the agent registered for the session, and Tilde asks in the
// browser.
//
// Like every polkit agent, it never checks a password itself: it runs
// polkit's setuid polkit-agent-helper-1, which runs PAM (service polkit-1)
// and gives polkit the verdict. The agent only carries the conversation,
// as the session helper does for logins (D43): the password answers PAM's
// first hidden prompt and any further question goes to the browser.

const (
	polkitName      = "org.freedesktop.PolicyKit1"
	polkitPath      = dbus.ObjectPath("/org/freedesktop/PolicyKit1/Authority")
	polkitAuthority = "org.freedesktop.PolicyKit1.Authority"
	agentIface      = "org.freedesktop.PolicyKit1.AuthenticationAgent"
	agentPath       = dbus.ObjectPath("/org/tilde/PolkitAgent")
	polkitCancelled = "org.freedesktop.PolicyKit1.Error.Cancelled"
	polkitFailed    = "org.freedesktop.PolicyKit1.Error.Failed"
	// maxAuthRequests bounds the requests waiting at once; polkit is asked
	// by programs the user runs, so this only stops a runaway loop.
	maxAuthRequests = 8
)

// polkitHelpers are where distributions install polkit-agent-helper-1.
var polkitHelpers = []string{
	"/usr/lib/polkit-1/polkit-agent-helper-1",
	"/usr/libexec/polkit-agent-helper-1",
	"/usr/lib/policykit-1/polkit-agent-helper-1",
}

// authTimeout dismisses a request nobody answers, as if cancelled, so a
// dialog does not greet the user hours later; authStepTimeout bounds one
// step of PAM. Tests shorten them.
var (
	authTimeout     = 5 * time.Minute
	authStepTimeout = 2 * time.Minute
)

// AuthRequest is polkit asking for a password, as the browser's dialog
// shows it.
type AuthRequest struct {
	ID string `json:"id"`
	// Action is polkit's action id, e.g. org.freedesktop.udisks2.filesystem-mount.
	Action string `json:"action"`
	// Message is polkit's text for the action, in the session's language.
	Message string `json:"message"`
	Icon    string `json:"icon,omitempty"`
	// Identities are the users whose password polkit accepts: the user
	// first when listed, else administrators.
	Identities []string `json:"identities"`
	// Prompt is a question PAM asks after the password (a verification
	// code), waiting for its answer.
	Prompt *protocol.Prompt `json:"prompt,omitempty"`
}

// polkitIdentity is polkit's (sa{sv}) identity: unix-user or unix-group.
type polkitIdentity struct {
	Kind    string
	Details map[string]dbus.Variant
}

type authRequest struct {
	AuthRequest // Prompt is guarded by the agent's mu
	cookie      string
	created     time.Time
	// ctx ends with the request, which kills its helper.
	ctx    context.Context
	cancel context.CancelFunc
	timer  *time.Timer
	done   chan struct{}
	result *dbus.Error // set before done is closed

	mu sync.Mutex   // one answer at a time
	h  *agentHelper // the helper waiting on a question, if any
}

type polkitAgent struct {
	user    string
	helper  string // polkit-agent-helper-1
	publish func()

	// audit reports authentications to the audit log (D46).
	audit func(protocol.AuditEvent)

	mu      sync.Mutex
	conn    *dbus.Conn // set once registered
	session string
	reqs    map[string]*authRequest // by ID
	closed  bool
}

func newPolkitAgent(user string, publish func()) *polkitAgent {
	return &polkitAgent{user: user, publish: publish, reqs: map[string]*authRequest{}}
}

// polkitSubject is polkit's (sa{sv}) subject.
type polkitSubject struct {
	Kind    string
	Details map[string]dbus.Variant
}

// start registers the agent for logind session id, a web session of this
// user (logind.go checked). Run calls it after the hello; without polkit
// or its helper, Tilde works as before and polkit refuses what needs a
// password.
func (a *polkitAgent) start(session string) {
	if a.helper == "" {
		for _, p := range polkitHelpers {
			if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSetuid != 0 {
				a.helper = p
				break
			}
		}
	}
	if a.helper == "" {
		slog.Info("worker: no polkit agent: polkit-agent-helper-1 not found")
		return
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		slog.Info("worker: no polkit agent", "err", err)
		return
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		conn.Close()
		return
	}
	a.conn = conn
	a.mu.Unlock()
	fail := func(err error) {
		slog.Info("worker: no polkit agent", "session", session, "err", err)
		a.mu.Lock()
		a.conn = nil
		a.mu.Unlock()
		conn.Close()
	}
	if err := conn.Export(agentObject{a}, agentPath, agentIface); err != nil {
		fail(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	subject := polkitSubject{"unix-session", map[string]dbus.Variant{"session-id": dbus.MakeVariant(session)}}
	locale := firstNonEmpty(os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG"), "C")
	if err := conn.Object(polkitName, polkitPath).CallWithContext(ctx, polkitAuthority+".RegisterAuthenticationAgent", 0,
		subject, locale, string(agentPath)).Err; err != nil {
		fail(err)
		return
	}
	a.mu.Lock()
	a.session = session
	a.mu.Unlock()
	slog.Info("worker: polkit agent registered", "session", session)
}

// running reports whether polkit asks this agent: only then may requests
// let udisks wait for a password.
func (a *polkitAgent) running() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session != "" && !a.closed
}

func (a *polkitAgent) close() {
	a.mu.Lock()
	a.closed = true
	conn, session := a.conn, a.session
	reqs := make([]*authRequest, 0, len(a.reqs))
	for _, r := range a.reqs {
		reqs = append(reqs, r)
	}
	a.mu.Unlock()
	for _, r := range reqs {
		a.finish(r, dbus.NewError(polkitCancelled, []any{"the session ended"}))
	}
	if conn == nil {
		return
	}
	if session != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		subject := polkitSubject{"unix-session", map[string]dbus.Variant{"session-id": dbus.MakeVariant(session)}}
		conn.Object(polkitName, polkitPath).CallWithContext(ctx, polkitAuthority+".UnregisterAuthenticationAgent", 0, subject, string(agentPath))
		cancel()
	}
	conn.Close()
}

// agentObject is what the agent exports on the bus: godbus exports every
// exported method, so this type has only polkit's two.
type agentObject struct{ a *polkitAgent }

// BeginAuthentication returns once the user authenticated (the helper told
// polkit) or the request ended; polkit waits for it.
func (o agentObject) BeginAuthentication(sender dbus.Sender, action, message, icon string, _ map[string]string,
	cookie string, ids []polkitIdentity) *dbus.Error {
	if !o.a.fromPolkit(sender) {
		return dbus.NewError(polkitFailed, []any{"only polkit may ask"})
	}
	r, derr := o.a.begin(action, message, icon, cookie, identityNames(ids, o.a.user))
	if derr != nil {
		return derr
	}
	<-r.done
	return r.result
}

func (o agentObject) CancelAuthentication(sender dbus.Sender, cookie string) *dbus.Error {
	if !o.a.fromPolkit(sender) {
		return dbus.NewError(polkitFailed, []any{"only polkit may cancel"})
	}
	o.a.mu.Lock()
	var found *authRequest
	for _, r := range o.a.reqs {
		if r.cookie == cookie {
			found = r
		}
	}
	o.a.mu.Unlock()
	if found != nil {
		o.a.finish(found, dbus.NewError(polkitCancelled, []any{"cancelled by polkit"}))
	}
	return nil
}

// fromPolkit: any user on the system bus can call the agent's methods, and
// would otherwise put a password dialog of their making in this user's
// browser, so calls count only from the bus name polkit owns.
func (a *polkitAgent) fromPolkit(sender dbus.Sender) bool {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	if conn == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var owner string
	err := conn.BusObject().CallWithContext(ctx, busDaemon+".GetNameOwner", 0, polkitName).Store(&owner)
	return err == nil && owner != "" && owner == string(sender)
}

// begin holds a new request for the browser.
func (a *polkitAgent) begin(action, message, icon, cookie string, names []string) (*authRequest, *dbus.Error) {
	if len(names) == 0 {
		return nil, dbus.NewError(polkitFailed, []any{"no user Tilde can ask for a password"})
	}
	b := make([]byte, 16)
	rand.Read(b)
	ctx, cancel := context.WithCancel(context.Background())
	r := &authRequest{
		AuthRequest: AuthRequest{ID: hex.EncodeToString(b), Action: action, Message: message, Icon: icon, Identities: names},
		cookie:      cookie,
		created:     time.Now(),
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	a.mu.Lock()
	if a.closed || len(a.reqs) >= maxAuthRequests {
		a.mu.Unlock()
		cancel()
		return nil, dbus.NewError(polkitFailed, []any{"too many authentication requests"})
	}
	a.reqs[r.ID] = r
	r.timer = time.AfterFunc(authTimeout, func() {
		a.finish(r, dbus.NewError(polkitCancelled, []any{"nobody answered"}))
	})
	a.mu.Unlock()
	slog.Info("worker: polkit asks for a password", "action", action)
	a.publish()
	return r, nil
}

// finish ends r with result (nil: authenticated) and kills its helper.
func (a *polkitAgent) finish(r *authRequest, result *dbus.Error) {
	a.mu.Lock()
	if a.reqs[r.ID] != r {
		a.mu.Unlock()
		return
	}
	delete(a.reqs, r.ID)
	r.timer.Stop()
	r.result = result
	r.cancel()
	close(r.done)
	a.mu.Unlock()
	a.publish()
}

func (a *polkitAgent) get(id string) *authRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reqs[id]
}

// list returns the waiting requests, oldest first.
func (a *polkitAgent) list() []AuthRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*authRequest, 0, len(a.reqs))
	for _, r := range a.reqs {
		out = append(out, r)
	}
	slices.SortFunc(out, func(x, y *authRequest) int { return x.created.Compare(y.created) })
	res := make([]AuthRequest, len(out))
	for i, r := range out {
		res[i] = r.AuthRequest
	}
	return res
}

func (a *polkitAgent) setPrompt(r *authRequest, p *protocol.Prompt) {
	a.mu.Lock()
	r.Prompt = p
	a.mu.Unlock()
	a.publish()
}

var (
	errNoAuthRequest = errors.New("this authentication request has ended")
	errAuthBusy      = errors.New("still checking the last answer")
)

// authFailed: PAM said no; the request stays open for another try.
type authFailed struct{ info []string }

func (e authFailed) Error() string {
	if len(e.info) > 0 {
		return strings.Join(e.info, " ")
	}
	return "Sorry, that didn't work. Please try again."
}

// respond passes one answer to request id. With no question waiting, the
// answer is identity's password and starts a check. It returns PAM's next
// question, or nil once polkit has the user's authorization.
func (a *polkitAgent) respond(id, identity, answer, rhost string) (*protocol.Prompt, error) {
	r := a.get(id)
	if r == nil {
		return nil, errNoAuthRequest
	}
	if !r.mu.TryLock() {
		return nil, errAuthBusy
	}
	defer r.mu.Unlock()
	if r.ctx.Err() != nil {
		return nil, errNoAuthRequest
	}
	if r.h == nil {
		if identity == "" {
			identity = r.Identities[0]
		}
		if !slices.Contains(r.Identities, identity) {
			return nil, badRequest{"polkit does not accept that user's password here"}
		}
		h, err := startAgentHelper(r.ctx, a.helper, identity, r.cookie)
		if err != nil {
			return nil, err
		}
		h.password = answer
		r.h = h
	} else {
		a.setPrompt(r, nil)
		if err := r.h.send(answer); err != nil {
			r.h.kill()
			r.h = nil
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(r.ctx, authStepTimeout)
	defer cancel()
	h := r.h
	for {
		var l helperLine
		var ok bool
		select {
		case l, ok = <-h.lines:
		case <-ctx.Done():
		}
		if !ok {
			// Killed, timed out, or gone without a verdict.
			h.kill()
			r.h = nil
			if r.ctx.Err() != nil {
				return nil, errNoAuthRequest
			}
			slog.Warn("worker: polkit-agent-helper-1 ended without a verdict", "action", r.Action)
			return nil, authFailed{h.info}
		}
		switch l.kind {
		case "PAM_PROMPT_ECHO_OFF", "PAM_PROMPT_ECHO_ON":
			echo := l.kind == "PAM_PROMPT_ECHO_ON"
			if auto, ok := h.autoAnswer(l.text, echo); ok {
				if err := h.send(auto); err != nil {
					h.kill()
					r.h = nil
					return nil, err
				}
				continue
			}
			p := &protocol.Prompt{Text: strings.TrimSpace(l.text), Echo: echo, Info: h.info}
			h.info = nil
			a.setPrompt(r, p)
			return p, nil
		case "PAM_ERROR_MSG", "PAM_TEXT_INFO":
			if t := strings.TrimSpace(l.text); t != "" && len(h.info) < 8 {
				h.info = append(h.info, t)
			}
		case "SUCCESS":
			h.kill() // it lingers two seconds after the verdict
			r.h = nil
			slog.Info("worker: polkit authorized", "action", r.Action, "as", h.identity)
			a.report(rhost, r.Action, h.identity, "granted")
			a.finish(r, nil)
			return nil, nil
		case "FAILURE":
			h.kill()
			r.h = nil
			slog.Warn("worker: polkit authentication failed", "action", r.Action, "as", h.identity)
			a.report(rhost, r.Action, h.identity, "failed")
			return nil, authFailed{h.info}
		}
	}
}

func (a *polkitAgent) report(rhost, action, identity, result string) {
	if a.audit != nil {
		a.audit(protocol.AuditEvent{Event: "polkit", Rhost: rhost, Action: action, Identity: identity, Result: result})
	}
}

// cancelRequest ends request id at the user's request.
func (a *polkitAgent) cancelRequest(id string) error {
	r := a.get(id)
	if r == nil {
		return errNoAuthRequest
	}
	a.finish(r, dbus.NewError(polkitCancelled, []any{"dismissed by the user"}))
	return nil
}

// helperLine is one line from polkit-agent-helper-1: a PAM_* message with
// its text, or the verdict SUCCESS or FAILURE.
type helperLine struct{ kind, text string }

// agentHelper is one run of polkit-agent-helper-1: one PAM conversation.
type agentHelper struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	cancel context.CancelFunc
	lines  chan helperLine // closed when the helper's output ends

	identity     string
	password     string
	passwordUsed bool
	info         []string
}

// startAgentHelper runs the helper for user, as libpolkit-agent does: the
// user name as its argument, the cookie on stdin so other users cannot
// read it from the command line.
func startAgentHelper(parent context.Context, path, user, cookie string) (*agentHelper, error) {
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, path, user)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	h := &agentHelper{cmd: cmd, stdin: stdin, cancel: cancel, lines: make(chan helperLine), identity: user}
	go h.read(ctx, stdout)
	if err := h.send(cookie); err != nil {
		h.kill()
		return nil, err
	}
	return h, nil
}

func (h *agentHelper) read(ctx context.Context, out io.Reader) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		kind, text, _ := strings.Cut(sc.Text(), " ")
		select {
		case h.lines <- helperLine{kind, strcompress(text)}:
		case <-ctx.Done():
		}
	}
	close(h.lines)
	io.Copy(io.Discard, out) // so it can exit after a line too long to read
	h.cmd.Wait()
}

// maxAnswer is PAM_MAX_RESP_SIZE less the line break: the helper reads
// lines into a buffer that size, and the rest of a longer answer would
// answer PAM's next question.
const maxAnswer = 511

func (h *agentHelper) send(line string) error {
	if strings.ContainsAny(line, "\n\x00") {
		return badRequest{"an answer cannot contain a line break"}
	}
	if len(line) > maxAnswer {
		return badRequest{"the answer is too long"}
	}
	_, err := io.WriteString(h.stdin, line+"\n")
	return err
}

// kill stops the helper. A setuid program keeps the user's real uid, so
// the worker may signal it.
func (h *agentHelper) kill() {
	h.cancel()
	h.stdin.Close()
}

// autoAnswer answers what the browser was already given: the password, to
// PAM's first hidden prompt, and the user name if asked before it.
func (h *agentHelper) autoAnswer(text string, echo bool) (string, bool) {
	if h.passwordUsed {
		return "", false
	}
	if !echo {
		h.passwordUsed = true
		p := h.password
		h.password = ""
		return p, true
	}
	if protocol.IsUserPrompt(text) {
		return h.identity, true
	}
	return "", false
}

// strcompress undoes the helper's g_strescape: C escapes, octal included.
func strcompress(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 == len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch c = s[i]; c {
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			n := 0
			j := i
			for ; j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7'; j++ {
				n = n*8 + int(s[j]-'0')
			}
			b.WriteByte(byte(n))
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// groupFile is read for the members of unix-group identities; tests
// replace it.
var groupFile = "/etc/group"

// identityNames turns polkit's identities into user names: unix-user as
// is, unix-group as its members (as GNOME's and Cinnamon's agents do). The
// user comes first, so typing one's own password is the default.
func identityNames(ids []polkitIdentity, self string) []string {
	var names []string
	add := func(n string) {
		if n != "" && !slices.Contains(names, n) && len(names) < 32 {
			names = append(names, n)
		}
	}
	for _, id := range ids {
		switch id.Kind {
		case "unix-user":
			if uid, ok := id.Details["uid"].Value().(uint32); ok {
				if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
					add(u.Username)
				}
			}
		case "unix-group":
			if gid, ok := id.Details["gid"].Value().(uint32); ok {
				for _, m := range groupMembers(gid) {
					add(m)
				}
			}
		}
	}
	if i := slices.Index(names, self); i > 0 {
		names = slices.Insert(slices.Delete(names, i, i+1), 0, self)
	}
	return names
}

func groupMembers(gid uint32) []string {
	data, err := os.ReadFile(groupFile)
	if err != nil {
		return nil
	}
	want := strconv.FormatUint(uint64(gid), 10)
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) == 4 && f[2] == want && f[3] != "" {
			return strings.Split(f[3], ",")
		}
	}
	return nil
}

func (w *Worker) handlePolkit(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{"agent": w.polkit.running(), "requests": w.polkit.list()})
}

// handlePolkitRespond passes the browser's password or answer on. The
// answer is never logged.
func (w *Worker) handlePolkitRespond(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string `json:"id"`
		Identity string `json:"identity"`
		Answer   string `json:"answer"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	p, err := w.polkit.respond(req.ID, req.Identity, req.Answer, r.Header.Get(protocol.ClientHeader))
	req.Answer = ""
	var failed authFailed
	switch {
	case err == nil && p == nil:
		rw.WriteHeader(http.StatusNoContent)
	case err == nil:
		writeJSON(rw, http.StatusOK, map[string]*protocol.Prompt{"prompt": p})
	case errors.As(err, &failed):
		writeJSON(rw, http.StatusUnauthorized, apiError{Error: failed.Error(), Code: "auth_failed"})
	case errors.Is(err, errNoAuthRequest):
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_found"})
	case errors.Is(err, errAuthBusy):
		writeJSON(rw, http.StatusConflict, apiError{Error: err.Error(), Code: "busy"})
	default:
		writeErr(rw, err)
	}
}

func (w *Worker) handlePolkitCancel(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	if err := w.polkit.cancelRequest(req.ID); err != nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_found"})
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}
