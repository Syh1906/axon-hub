package responses

// The terminal event must agree with the response status, including when the
// upstream response passed through another API format before reaching us.
func responseTerminalEventType(status *string) StreamEventType {
	if status != nil {
		switch *status {
		case "failed":
			return StreamEventTypeResponseFailed
		case "incomplete":
			return StreamEventTypeResponseIncomplete
		case "canceled", "cancelled":
			return StreamEventTypeResponseCancelled
		}
	}
	return StreamEventTypeResponseCompleted
}
