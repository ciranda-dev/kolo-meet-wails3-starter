// A stand-in for the generated bindings and the Wails runtime, for test/crash.test.mjs: it records every method
// called, answers from `answers`, and sends events with `emit`.
export const calls = []
export const answers = {}
export const copied = []
const handlers = new Map()

export function emit(name, data) {
  for (const fn of handlers.get(name) ?? []) {
    fn({ name, data })
  }
}

export const Events = {
  On(name, fn) {
    handlers.set(name, [...(handlers.get(name) ?? []), fn])
    return () => handlers.set(name, (handlers.get(name) ?? []).filter((x) => x !== fn))
  },
}

export const Clipboard = {
  SetText: async (text) => void copied.push(text),
  Text: async () => '',
}

const method = (name) => async (...args) => {
  calls.push([name, ...args])
  // A new copy each time, as the bindings make one.
  return structuredClone(answers[name] ?? null)
}

export const Start = method('Start')
export const Info = method('Info')
export const Settings = method('Settings')
export const Devices = method('Devices')
export const Lobby = method('Lobby')
export const History = method('History')
export const Call = method('Call')
export const CreateCall = method('CreateCall')
export const JoinCall = method('JoinCall')
export const Notch = method('Notch')
export const LeaveCall = method('LeaveCall')
export const NotchLeave = method('NotchLeave')
export const SetCamera = method('SetCamera')
export const SetMicrophone = method('SetMicrophone')
export const ReportError = method('ReportError')
