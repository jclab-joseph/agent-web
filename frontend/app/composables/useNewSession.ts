/** Opens the new-session dialog, optionally with a working directory. */
export function useNewSession() {
  const state = useState('newSession', () => ({ visible: false, cwd: '' }))
  return {
    state,
    open(cwd = '') {
      state.value = { visible: true, cwd }
    },
    close() {
      state.value = { ...state.value, visible: false }
    },
  }
}
