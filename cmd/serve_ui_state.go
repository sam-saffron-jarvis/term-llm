package cmd

func (rt *serveRuntime) hasActiveRun() bool {
	rt.interruptMu.Lock()
	mainRunning := rt.activeInterrupt != nil
	rt.interruptMu.Unlock()
	rt.sideQuestion.mu.Lock()
	sideRunning := rt.sideQuestion.running
	rt.sideQuestion.mu.Unlock()
	return mainRunning || sideRunning
}

func (rt *serveRuntime) clearLastUIRunError() {
	rt.uiStateMu.Lock()
	defer rt.uiStateMu.Unlock()
	rt.lastUIRunError = ""
}

func (rt *serveRuntime) setLastUIRunError(message string) {
	rt.uiStateMu.Lock()
	defer rt.uiStateMu.Unlock()
	rt.lastUIRunError = message
}

func (rt *serveRuntime) consumeLastUIRunError() string {
	rt.uiStateMu.Lock()
	defer rt.uiStateMu.Unlock()
	message := rt.lastUIRunError
	rt.lastUIRunError = ""
	return message
}
