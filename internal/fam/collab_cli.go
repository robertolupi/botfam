package fam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func parseJSONMap(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(s), &res); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return res, nil
}

func getActorAndWorkDir(explicitActor string) (string, string, error) {
	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return "", "", err
	}
	actor := explicitActor
	if actor == "" {
		actor = os.Getenv("COLLAB_ACTOR")
	}
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}
	return actor, info.Root, nil
}

// SendCmd sends a message to another actor.
func SendCmd(args []string, out io.Writer) error {
	var to, msgType, payloadStr, inReplyTo, actorInput string
	var expiresAtVal float64
	var expiresAtPtr *float64

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--to="):
			to = strings.TrimPrefix(arg, "--to=")
		case arg == "--to":
			i++
			if i < len(args) {
				to = args[i]
			}
		case strings.HasPrefix(arg, "--type="):
			msgType = strings.TrimPrefix(arg, "--type=")
		case arg == "--type":
			i++
			if i < len(args) {
				msgType = args[i]
			}
		case strings.HasPrefix(arg, "--payload="):
			payloadStr = strings.TrimPrefix(arg, "--payload=")
		case arg == "--payload":
			i++
			if i < len(args) {
				payloadStr = args[i]
			}
		case strings.HasPrefix(arg, "--in-reply-to="):
			inReplyTo = strings.TrimPrefix(arg, "--in-reply-to=")
		case arg == "--in-reply-to":
			i++
			if i < len(args) {
				inReplyTo = args[i]
			}
		case strings.HasPrefix(arg, "--expires-at="):
			val := strings.TrimPrefix(arg, "--expires-at=")
			parsed, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid expires-at float %q: %w", val, err)
			}
			expiresAtVal = parsed
			expiresAtPtr = &expiresAtVal
		case arg == "--expires-at":
			i++
			if i < len(args) {
				parsed, err := strconv.ParseFloat(args[i], 64)
				if err != nil {
					return fmt.Errorf("invalid expires-at float %q: %w", args[i], err)
				}
				expiresAtVal = parsed
				expiresAtPtr = &expiresAtVal
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown send argument %q", arg)
		}
	}

	if to == "" {
		return errors.New("missing required --to <actor>")
	}
	if msgType == "" {
		return errors.New("missing required --type <type>")
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	payload, err := parseJSONMap(payloadStr)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir":    workDir,
		"actor":       actor,
		"to":          to,
		"type":        msgType,
		"payload":     payload,
		"in_reply_to": inReplyTo,
		"expires_at":  expiresAtPtr,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "send", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Sent message: ID=%v to=%v type=%v\n", result["id"], result["to"], result["type"])
	return nil
}

// RecvCmd blocks until a matching message lands or timeout.
func RecvCmd(args []string, out io.Writer) error {
	var matchType, actorInput string
	var timeoutS float64 = 120

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--match-type="):
			matchType = strings.TrimPrefix(arg, "--match-type=")
		case arg == "--match-type":
			i++
			if i < len(args) {
				matchType = args[i]
			}
		case strings.HasPrefix(arg, "--timeout="):
			val := strings.TrimPrefix(arg, "--timeout=")
			parsed, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid timeout float %q: %w", val, err)
			}
			timeoutS = parsed
		case arg == "--timeout":
			i++
			if i < len(args) {
				parsed, err := strconv.ParseFloat(args[i], 64)
				if err != nil {
					return fmt.Errorf("invalid timeout float %q: %w", args[i], err)
				}
				timeoutS = parsed
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown recv argument %q", arg)
		}
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir":   workDir,
		"actor":      actor,
		"match_type": matchType,
		"timeout_s":  timeoutS,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "recv", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	if len(result) == 0 {
		fmt.Fprintln(out, "No message received (timeout).")
		return nil
	}

	fmt.Fprintf(out, "Received message: ID=%v from=%v type=%v payload=%v\n", result["id"], result["from"], result["type"], result["payload"])
	return nil
}

// TryRecvCmd reserves the oldest matching message if present.
func TryRecvCmd(args []string, out io.Writer) error {
	var matchType, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--match-type="):
			matchType = strings.TrimPrefix(arg, "--match-type=")
		case arg == "--match-type":
			i++
			if i < len(args) {
				matchType = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown try-recv argument %q", arg)
		}
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir":   workDir,
		"actor":      actor,
		"match_type": matchType,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "try_recv", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	if len(result) == 0 {
		fmt.Fprintln(out, "No matching message present.")
		return nil
	}

	fmt.Fprintf(out, "Received message: ID=%v from=%v type=%v payload=%v\n", result["id"], result["from"], result["type"], result["payload"])
	return nil
}

// PeekCmd inspects the oldest matching message without reserving it.
func PeekCmd(args []string, out io.Writer) error {
	var matchType, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--match-type="):
			matchType = strings.TrimPrefix(arg, "--match-type=")
		case arg == "--match-type":
			i++
			if i < len(args) {
				matchType = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown peek argument %q", arg)
		}
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir":   workDir,
		"actor":      actor,
		"match_type": matchType,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "peek", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	if len(result) == 0 {
		fmt.Fprintln(out, "No matching message present.")
		return nil
	}

	fmt.Fprintf(out, "Peeked message: ID=%v from=%v type=%v payload=%v\n", result["id"], result["from"], result["type"], result["payload"])
	return nil
}

// AckCmd acks a reserved message.
func AckCmd(args []string, out io.Writer) error {
	var msgID, outcomeStr, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--id="):
			msgID = strings.TrimPrefix(arg, "--id=")
		case arg == "--id":
			i++
			if i < len(args) {
				msgID = args[i]
			}
		case strings.HasPrefix(arg, "--outcome="):
			outcomeStr = strings.TrimPrefix(arg, "--outcome=")
		case arg == "--outcome":
			i++
			if i < len(args) {
				outcomeStr = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown ack argument %q", arg)
		}
	}

	if msgID == "" {
		return errors.New("missing required --id <msg-id>")
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	outcome, err := parseJSONMap(outcomeStr)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"actor":    actor,
		"id":       msgID,
		"outcome":  outcome,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "ack", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Acked message ID %v\n", msgID)
	return nil
}

// SeenCmd checks whether a message id has been acked.
func SeenCmd(args []string, out io.Writer) error {
	var msgID, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--id="):
			msgID = strings.TrimPrefix(arg, "--id=")
		case arg == "--id":
			i++
			if i < len(args) {
				msgID = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown seen argument %q", arg)
		}
	}

	if msgID == "" {
		return errors.New("missing required --id <msg-id>")
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"actor":    actor,
		"id":       msgID,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "seen", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Message ID %v seen: %v\n", msgID, result["seen"])
	return nil
}

// InboxCmd shows mailbox and task counts.
func InboxCmd(args []string, out io.Writer) error {
	var actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown inbox argument %q", arg)
		}
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"actor":    actor,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "inbox", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	// Format nicely
	fmt.Fprintf(out, "Mailbox for %q:\n", actor)
	if cur, ok := result["cur"].([]any); ok {
		fmt.Fprintf(out, "  Standing Messages: %d\n", len(cur))
		for _, m := range cur {
			if mm, ok := m.(map[string]any); ok {
				fmt.Fprintf(out, "    - [%v] from %v (%v)\n", mm["id"], mm["from"], mm["type"])
			}
		}
	}
	if tasks, ok := result["tasks"].(map[string]any); ok {
		fmt.Fprintln(out, "Tasks:")
		fmt.Fprintf(out, "  Open: %v\n", tasks["open"])
		fmt.Fprintf(out, "  Done: %v\n", tasks["done"])
		if claimed, ok := tasks["claimed"].(map[string]any); ok {
			fmt.Fprintln(out, "  Claimed:")
			for owner, cnt := range claimed {
				fmt.Fprintf(out, "    - %v: %v\n", owner, cnt)
			}
		}
	}
	return nil
}

// PostCmd posts a task.
func PostCmd(args []string, out io.Writer) error {
	var taskType, payloadStr, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--type="):
			taskType = strings.TrimPrefix(arg, "--type=")
		case arg == "--type":
			i++
			if i < len(args) {
				taskType = args[i]
			}
		case strings.HasPrefix(arg, "--payload="):
			payloadStr = strings.TrimPrefix(arg, "--payload=")
		case arg == "--payload":
			i++
			if i < len(args) {
				payloadStr = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown post argument %q", arg)
		}
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	payload, err := parseJSONMap(payloadStr)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"actor":    actor,
		"type":     taskType,
		"payload":  payload,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "post", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Posted task ID %v (type: %v)\n", result["id"], result["type"])
	return nil
}

// ClaimCmd claims one open task.
func ClaimCmd(args []string, out io.Writer) error {
	var taskID, taskType, suggestedOwner, actorInput string
	var leaseTTL float64 = 120

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--lease-ttl="):
			val := strings.TrimPrefix(arg, "--lease-ttl=")
			parsed, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid lease-ttl float %q: %w", val, err)
			}
			leaseTTL = parsed
		case arg == "--lease-ttl":
			i++
			if i < len(args) {
				parsed, err := strconv.ParseFloat(args[i], 64)
				if err != nil {
					return fmt.Errorf("invalid lease-ttl float %q: %w", args[i], err)
				}
				leaseTTL = parsed
			}
		case strings.HasPrefix(arg, "--task-id="):
			taskID = strings.TrimPrefix(arg, "--task-id=")
		case arg == "--task-id":
			i++
			if i < len(args) {
				taskID = args[i]
			}
		case strings.HasPrefix(arg, "--type="):
			taskType = strings.TrimPrefix(arg, "--type=")
		case arg == "--type":
			i++
			if i < len(args) {
				taskType = args[i]
			}
		case strings.HasPrefix(arg, "--suggested-owner="):
			suggestedOwner = strings.TrimPrefix(arg, "--suggested-owner=")
		case arg == "--suggested-owner":
			i++
			if i < len(args) {
				suggestedOwner = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown claim argument %q", arg)
		}
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir":        workDir,
		"actor":           actor,
		"lease_ttl":       leaseTTL,
		"task_id":         taskID,
		"type":            taskType,
		"suggested_owner": suggestedOwner,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "claim", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	if len(result) == 0 {
		fmt.Fprintln(out, "No task claimed.")
		return nil
	}

	fmt.Fprintf(out, "Claimed task ID %v (type: %v)\n", result["id"], result["type"])
	return nil
}

// CompleteCmd completes an owned task.
func CompleteCmd(args []string, out io.Writer) error {
	var taskID, resultStr, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--task-id="):
			taskID = strings.TrimPrefix(arg, "--task-id=")
		case arg == "--task-id":
			i++
			if i < len(args) {
				taskID = args[i]
			}
		case strings.HasPrefix(arg, "--result="):
			resultStr = strings.TrimPrefix(arg, "--result=")
		case arg == "--result":
			i++
			if i < len(args) {
				resultStr = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown complete argument %q", arg)
		}
	}

	if taskID == "" {
		return errors.New("missing required --task-id <task-id>")
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	taskResult, err := parseJSONMap(resultStr)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"actor":    actor,
		"task_id":  taskID,
		"result":   taskResult,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "complete", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Completed task ID %v\n", taskID)
	return nil
}

// HeartbeatCmd extends an owned task lease.
func HeartbeatCmd(args []string, out io.Writer) error {
	var taskID, actorInput string
	var leaseTTL float64 = 120

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--task-id="):
			taskID = strings.TrimPrefix(arg, "--task-id=")
		case arg == "--task-id":
			i++
			if i < len(args) {
				taskID = args[i]
			}
		case strings.HasPrefix(arg, "--lease-ttl="):
			val := strings.TrimPrefix(arg, "--lease-ttl=")
			parsed, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid lease-ttl float %q: %w", val, err)
			}
			leaseTTL = parsed
		case arg == "--lease-ttl":
			i++
			if i < len(args) {
				parsed, err := strconv.ParseFloat(args[i], 64)
				if err != nil {
					return fmt.Errorf("invalid lease-ttl float %q: %w", args[i], err)
				}
				leaseTTL = parsed
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown heartbeat argument %q", arg)
		}
	}

	if taskID == "" {
		return errors.New("missing required --task-id <task-id>")
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir":  workDir,
		"actor":     actor,
		"task_id":   taskID,
		"lease_ttl": leaseTTL,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "heartbeat", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Heartbeat sent for task ID %v\n", taskID)
	return nil
}

// AbandonCmd releases an owned task back to open.
func AbandonCmd(args []string, out io.Writer) error {
	var taskID, reason, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--task-id="):
			taskID = strings.TrimPrefix(arg, "--task-id=")
		case arg == "--task-id":
			i++
			if i < len(args) {
				taskID = args[i]
			}
		case strings.HasPrefix(arg, "--reason="):
			reason = strings.TrimPrefix(arg, "--reason=")
		case arg == "--reason":
			i++
			if i < len(args) {
				reason = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown abandon argument %q", arg)
		}
	}

	if taskID == "" {
		return errors.New("missing required --task-id <task-id>")
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"actor":    actor,
		"task_id":  taskID,
		"reason":   reason,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "abandon", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Abandoned task ID %v\n", taskID)
	return nil
}

// SweepCmd returns expired claimed tasks to open.
func SweepCmd(args []string, out io.Writer) error {
	var actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown sweep argument %q", arg)
		}
	}

	_, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "sweep", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	if swept, ok := result["swept"].([]any); ok {
		fmt.Fprintf(out, "Swept %d expired task leases back to open.\n", len(swept))
	} else {
		fmt.Fprintln(out, "Sweep complete.")
	}
	return nil
}

// SessionAppendCmd appends an entry to a session log.
func SessionAppendCmd(args []string, out io.Writer) error {
	var session, body, handoffStr, actorInput string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--session="):
			session = strings.TrimPrefix(arg, "--session=")
		case arg == "--session":
			i++
			if i < len(args) {
				session = args[i]
			}
		case strings.HasPrefix(arg, "--body="):
			body = strings.TrimPrefix(arg, "--body=")
		case arg == "--body":
			i++
			if i < len(args) {
				body = args[i]
			}
		case strings.HasPrefix(arg, "--handoff="):
			handoffStr = strings.TrimPrefix(arg, "--handoff=")
		case arg == "--handoff":
			i++
			if i < len(args) {
				handoffStr = args[i]
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown session-append argument %q", arg)
		}
	}

	if session == "" {
		return errors.New("missing required --session <slug>")
	}
	if body == "" {
		return errors.New("missing required --body <content>")
	}

	actor, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	handoff, err := parseJSONMap(handoffStr)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"actor":    actor,
		"session":  session,
		"body":     body,
		"handoff":  handoff,
	}

	var result map[string]any
	err = sendDaemonRequest(context.Background(), "session_append", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Appended entry ID %v to session %q\n", result["id"], session)
	return nil
}

// SessionReadCmd reads entries from a session log.
func SessionReadCmd(args []string, out io.Writer) error {
	var session, fromFilter, actorInput string
	var sinceTS float64
	var limit int

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, "--session="):
			session = strings.TrimPrefix(arg, "--session=")
		case arg == "--session":
			i++
			if i < len(args) {
				session = args[i]
			}
		case strings.HasPrefix(arg, "--from="):
			fromFilter = strings.TrimPrefix(arg, "--from=")
		case arg == "--from":
			i++
			if i < len(args) {
				fromFilter = args[i]
			}
		case strings.HasPrefix(arg, "--since="):
			val := strings.TrimPrefix(arg, "--since=")
			parsed, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid since float %q: %w", val, err)
			}
			sinceTS = parsed
		case arg == "--since":
			i++
			if i < len(args) {
				parsed, err := strconv.ParseFloat(args[i], 64)
				if err != nil {
					return fmt.Errorf("invalid since float %q: %w", args[i], err)
				}
				sinceTS = parsed
			}
		case strings.HasPrefix(arg, "--limit="):
			val := strings.TrimPrefix(arg, "--limit=")
			parsed, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid limit int %q: %w", val, err)
			}
			limit = parsed
		case arg == "--limit":
			i++
			if i < len(args) {
				parsed, err := strconv.Atoi(args[i])
				if err != nil {
					return fmt.Errorf("invalid limit int %q: %w", args[i], err)
				}
				limit = parsed
			}
		case strings.HasPrefix(arg, "--actor="):
			actorInput = strings.TrimPrefix(arg, "--actor=")
		case arg == "--actor":
			i++
			if i < len(args) {
				actorInput = args[i]
			}
		default:
			return fmt.Errorf("unknown session-read argument %q", arg)
		}
	}

	if session == "" {
		return errors.New("missing required --session <slug>")
	}

	_, workDir, err := getActorAndWorkDir(actorInput)
	if err != nil {
		return err
	}

	reqPayload := map[string]any{
		"work_dir": workDir,
		"session":  session,
		"from":     fromFilter,
		"since_ts": sinceTS,
		"limit":    limit,
	}

	var result []any
	err = sendDaemonRequest(context.Background(), "session_read", reqPayload, &result)
	if err != nil {
		return err
	}

	if IsJSONOutput() {
		return writeJSONOutput(out, result)
	}

	fmt.Fprintf(out, "Session Entries for %q:\n", session)
	for _, e := range result {
		if entry, ok := e.(map[string]any); ok {
			fmt.Fprintf(out, "  - [%v] %v (ts: %v):\n      %v\n", entry["id"], entry["actor"], entry["ts"], entry["body"])
		}
	}
	return nil
}
