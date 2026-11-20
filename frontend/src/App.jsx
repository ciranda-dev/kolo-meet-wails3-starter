import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react'
import { call, on, copyText, pasteText } from './service.js'
import { I18n } from './context.js'
import { htmlLang, locales, pickLocale, translate } from './i18n.js'
import { looksLikeInvite, safeText } from './format.js'
import { errorSentence, meetError } from './errors.js'
import { callReducer, noCall } from './calls.js'
import { relaySummary } from './relays.js'
import { fixCommand } from './command.js'
import { createGate, newerNetwork } from './fresh.js'
import { loadPrefs, savePrefs } from './prefs.js'
import { Sidebar } from './Sidebar.jsx'
import { Modal } from './Modal.jsx'
import { Fatal, Home, NameForm } from './Home.jsx'
import { Lobby } from './Lobby.jsx'
import { Joining, phaseText } from './Joining.jsx'
import { CallView } from './CallView.jsx'
import { PeopleList } from './People.jsx'
import { Ended } from './Ended.jsx'
import { InviteDialog, JoinForm, SecurityDialog } from './Dialogs.jsx'
import { DeviceHelp } from './DeviceHelp.jsx'
import { HistoryList } from './History.jsx'
import { SettingsPanel } from './Settings.jsx'
import { CopyButton } from './parts.jsx'
import { barTitleKey, canStartCall, networkLine, stateTone } from './shell.js'
import { readCollapsed, writeCollapsed } from './sidebar.js'
import { useNarrow } from './narrow.js'
import './meet.css'

const OFF = { state: 'off', deviceId: '', error: null }
const NO_DEVICES = { cameras: [], microphones: [], speakers: [] }
const PENDING = ['creating', 'joining', 'connecting']
const METHOD = { camera: 'SetCamera', microphone: 'SetMicrophone' }
const TOAST_MS = 6000

function App() {
  const [prefs, setPrefs] = useState(loadPrefs)
  const locale = pickLocale(prefs.lang, window.navigator.languages ?? [])
  const theme = prefs.theme === 'light' || prefs.theme === 'dark' ? prefs.theme : 'system'
  const lang = htmlLang[locale]
  const t = useCallback((key, vars) => translate(locale, key, vars), [locale])
  const fail = useCallback((err, fallback) => errorSentence(t, locale, err, fallback), [t, locale])
  const i18n = useMemo(() => ({ t, fail, lang }), [t, fail, lang])

  const [info, setInfo] = useState(null)
  const [network, setNetwork] = useState({ state: 'starting', message: '', error: null, mode: 'public', entryNodes: 0, firstNode: '', relays: [], seq: 0 })
  const [fatal, setFatal] = useState(null)
  const [settings, setSettings] = useState(null)
  const [devices, setDevices] = useState(NO_DEVICES)
  const [lobby, setLobby] = useState({ microphone: OFF, camera: OFF, speaker: OFF })
  const [history, setHistory] = useState([])
  // Every call that reaches the window goes through the reducer, which drops what is stale.
  const [{ call: current }, dispatch] = useReducer(callReducer, noCall)
  const [intent, setIntent] = useState('')
  // A pasted invite lives here for one attempt: never in storage, the URL, the title or a log.
  const [invite, setInvite] = useState('')
  const [dialog, setDialog] = useState('')
  const [joinError, setJoinError] = useState(null)
  const [lobbyError, setLobbyError] = useState(null)
  const [busy, setBusy] = useState(false)
  const [leaving, setLeaving] = useState(false)
  const [leaveError, setLeaveError] = useState(null)
  // Remove asks first: the ID waiting for a yes, the call that runs, and why it failed.
  const [removeAsk, setRemoveAsk] = useState('')
  const [removing, setRemoving] = useState(false)
  const [removeError, setRemoveError] = useState(null)
  // The microphone permission as the system reports it; "unknown" where it cannot say.
  const [access, setAccess] = useState('unknown')
  const [asking, setAsking] = useState(false)
  const [toasts, setToasts] = useState([])
  const [spoken, setSpoken] = useState('')
  const [collapsed, setCollapsed] = useState(() => readCollapsed())
  const narrow = useNarrow()
  const invited = useRef('')
  const returnTo = useRef(null)
  const pageButtons = { history: useRef(null), settings: useRef(null) }
  const said = useRef('')
  // The devices and the history have events but no seq: a getter's answer that crossed an event is dropped.
  const lobbyGate = useState(createGate)[0]
  const historyGate = useState(createGate)[0]

  const update = (patch) => {
    setPrefs((prev) => {
      const next = { ...prev, ...patch }
      savePrefs(next)
      return next
    })
  }

  useEffect(() => {
    document.documentElement.lang = lang
    document.title = t('app.title')
    if (theme === 'system') {
      delete document.documentElement.dataset.theme
    } else {
      document.documentElement.dataset.theme = theme
    }
  }, [lang, theme, t])

  const refreshDevices = useCallback(() => call('Devices').then((list) => setDevices(list ?? NO_DEVICES)).catch(() => {}), [])
  const refreshLobby = useCallback(() => {
    const mark = lobbyGate.mark()
    return call('Lobby').then((now) => now && lobbyGate.fresh(mark) && setLobby(now)).catch(() => {})
  }, [lobbyGate])

  useEffect(() => {
    const offs = [
      on('meet:call', (next) => dispatch({ type: 'call', call: next })),
      on('meet:lobby', (next) => {
        lobbyGate.event()
        if (next) {
          setLobby(next)
        }
      }),
      on('meet:network', (next) => setNetwork((prev) => newerNetwork(prev, next))),
      on('meet:history', (items) => {
        historyGate.event()
        setHistory(items ?? [])
      }),
      on('meet:participant', (event) => {
        if (event?.kind !== 'left') {
          return
        }
        const id = `${event.id}:${Date.now()}`
        setToasts((prev) => [...prev.slice(-2), { id, name: event.name }])
        window.setTimeout(() => setToasts((prev) => prev.filter((x) => x.id !== id)), TOAST_MS)
      }),
    ]
    let marks = []
    call('Start')
      .then((started) => {
        setInfo(started)
        // Start's network is a snapshot: an event that arrived first may be newer (seq).
        setNetwork((prev) => newerNetwork(prev, started?.network))
        marks = [lobbyGate.mark(), historyGate.mark()]
        return Promise.all([call('Settings'), call('Devices'), call('Lobby'), call('History'), call('Call')])
      })
      .then(([saved, list, now, items, running]) => {
        setSettings(saved)
        setDevices(list ?? NO_DEVICES)
        if (now && lobbyGate.fresh(marks[0])) {
          setLobby(now)
        }
        if (historyGate.fresh(marks[1])) {
          setHistory(items ?? [])
        }
        if (running) {
          dispatch({ type: 'call', call: running })
        }
      })
      .catch(setFatal)
    return () => offs.forEach((off) => off())
    // The service is started once; the handlers only use state setters.
  }, [])

  const state = current?.state ?? ''
  const active = state === 'active'
  const pending = PENDING.includes(state)
  const ended = state === 'ended'
  const ready = !fatal && network.state === 'ready'
  const named = Boolean(settings?.displayName)

  // A join or a start that the person cancelled goes back to the lobby without an end screen.
  useEffect(() => {
    if (ended) {
      setLeaving(false)
      setLeaveError(null)
      setDialog((open) => (open === 'invite' || open === 'security' || open === 'people' || open.startsWith('help-') ? '' : open))
      refreshLobby()
      if (!current.error && !(current.started > 0)) {
        dispatch({ type: 'dismiss' })
      }
    }
  }, [ended, current, refreshLobby])

  // The host sees the invite once, when the call is ready and nobody else is in it.
  useEffect(() => {
    if (active && current.role === 'host' && current.invite && current.participants.length <= 1 && invited.current !== current.id) {
      invited.current = current.id
      setDialog('invite')
    }
  }, [active, current])

  // One polite sentence per change of state or of the number of people.
  useEffect(() => {
    if (!current) {
      return
    }
    const key = `${current.id}:${current.state}:${current.phase}:${current.participants.length}`
    if (said.current === key) {
      return
    }
    const first = !said.current.startsWith(`${current.id}:active:`)
    said.current = key
    if (PENDING.includes(current.state)) {
      setSpoken(phaseText(t, current))
    } else if (current.state === 'active') {
      setSpoken(first ? t('call.live.active') : t('call.live.people', { n: current.participants.length }))
    } else if (current.state === 'ended') {
      setSpoken(fail(current.error, t('ended.left')))
    }
  }, [current, t, fail])

  const toggle = useCallback((device, wanted) => {
    update({ [`${device}Asked`]: true })
    const mark = lobbyGate.mark()
    return call(METHOD[device], wanted)
      .then((media) => {
        if (lobbyGate.fresh(mark)) {
          setLobby((prev) => ({ ...prev, [device]: media ?? prev[device] }))
        }
        if (device === 'microphone' && wanted) {
          setAccess((prev) => (prev === 'not-determined' ? 'authorized' : prev))
        }
        update({ [`${device}On`]: wanted })
      })
      .catch(() => refreshLobby())
    // `update` only wraps a state setter.
  }, [refreshLobby, lobbyGate])

  const openLobby = (next) => {
    setIntent(next)
    setLobbyError(null)
    refreshDevices()
    // Reading the permission opens no device and asks nothing.
    call('MicrophoneAccess').then((status) => setAccess(status || 'unknown')).catch(() => setAccess('unknown'))
    // The person had these on the last time, so they come back on; nothing else opens a device.
    for (const device of ['microphone', 'camera']) {
      if (prefs[`${device}On`] && lobby[device].state === 'off') {
        toggle(device, true)
      }
    }
  }

  const askMicrophone = () => {
    setAsking(true)
    call('RequestMicrophone')
      .then((status) => setAccess(status || 'unknown'))
      .catch((err) => {
        const reason = meetError(err)?.reason
        if (reason === 'microphone-denied' || reason === 'microphone-restricted') {
          setAccess(reason.slice(11))
        }
      })
      .finally(() => setAsking(false))
  }

  const closeLobby = () => {
    for (const device of ['microphone', 'camera']) {
      if (lobby[device].state !== 'off') {
        call(METHOD[device], false).then(refreshLobby).catch(() => {})
      }
    }
    setIntent('')
    setInvite('')
    setLobbyError(null)
  }

  const saveName = (name) =>
    call('SetDisplayName', name).then((next) => {
      setSettings(next)
      return next
    })

  const saveDevices = (ids) =>
    call('SetDevices', ids.cameraId ?? '', ids.microphoneId ?? '', ids.speakerId ?? '').then((next) => {
      setSettings(next)
      return next
    })

  const begin = async (name, way = intent, text = invite) => {
    setBusy(true)
    setLobbyError(null)
    try {
      if (name !== undefined && name !== settings.displayName) {
        await saveName(name)
      }
      const started = way === 'join' ? await call('JoinCall', text) : await call('CreateCall')
      dispatch({ type: 'call', call: started })
    } catch (err) {
      if (way === 'join' && meetError(err)?.kind === 'invalid-invite') {
        setJoinError(err)
        setDialog('join')
      } else {
        setLobbyError(err)
      }
    } finally {
      setBusy(false)
    }
  }

  const submitInvite = () => {
    if (!invite.trim()) {
      setJoinError({ key: 'join.needInvite' })
      return
    }
    if (!looksLikeInvite(invite)) {
      setJoinError({ reason: 'invalid-invite', kind: 'invalid-invite', detail: '', cause: '', message: '' })
      return
    }
    setJoinError(null)
    setDialog('')
    if (intent !== 'join') {
      openLobby('join')
    }
  }

  const leave = () => {
    setLeaving(true)
    setLeaveError(null)
    // A teardown that takes too long is a rejection (call-still-ending): say it and let the person try again.
    call('LeaveCall')
      .catch((err) => setLeaveError(meetError(err)?.reason === 'no-call' ? null : err))
      .finally(() => setLeaving(false))
  }

  const openPeople = () => {
    setRemoveAsk('')
    setRemoveError(null)
    setDialog('people')
  }

  const remove = (id) => {
    setRemoving(true)
    setRemoveError(null)
    call('RemoveParticipant', id)
      .then(() => setRemoveAsk(''))
      .catch(setRemoveError)
      .finally(() => setRemoving(false))
  }

  const home = () => {
    dispatch({ type: 'dismiss' })
    closeLobby()
  }

  const retry = () => {
    dispatch({ type: 'dismiss' })
    setIntent('join')
    begin(undefined, 'join', invite)
  }

  const openJoin = (opener) => {
    returnTo.current = opener
    setJoinError(null)
    setDialog('join')
  }

  const otherInvite = (opener) => {
    dispatch({ type: 'dismiss' })
    setIntent('join')
    setInvite('')
    openJoin(opener)
  }

  // Closing the dialog with nothing pasted leaves the lobby that was only waiting for it.
  const closeJoin = () => {
    setDialog('')
    if (!invite.trim()) {
      closeLobby()
    }
  }

  const openSettings = () => {
    call('Settings').then(setSettings).catch(() => {})
    refreshDevices()
    setDialog('settings')
  }

  const toggleSidebar = () => {
    writeCollapsed(!collapsed)
    setCollapsed(!collapsed)
  }

  const openPage = (name) => {
    returnTo.current = pageButtons[name].current
    if (name === 'settings') {
      openSettings()
    } else {
      setDialog('history')
    }
  }

  // "+": a finished call goes back to the start first; Start opens the lobby, Join its dialog.
  const choose = (kind, opener) => {
    if (ended) {
      dispatch({ type: 'dismiss' })
    }
    if (kind === 'join') {
      openJoin(opener)
    } else {
      setInvite('')
      openLobby('start')
    }
  }

  const copyInvite = () => call('CopyInvite').then(() => setSpoken(t('invite.copied.live')))
  const copyMessage = () => copyText(`${t('invite.message')}\n\n${current?.invite ?? ''}`).then(() => setSpoken(t('invite.copied.live')))
  const newInvite = () => {
    const id = current?.id
    return call('NewInvite').then((text) => dispatch({ type: 'invite', id, invite: text }))
  }

  const media = active ? current : lobby
  const helpDevice = dialog.startsWith('help-') ? dialog.slice(5) : ''
  const people = ended ? history.find((x) => x.id === current.id)?.participants ?? 0 : 0
  const inCall = active || pending
  const clearHistory = () => call('ClearHistory').catch(() => {})
  const view = fatal ? 'fatal' : !settings ? 'loading' : active ? 'call' : pending ? 'joining' : ended ? 'ended' : !named ? 'name' : intent ? 'lobby' : 'home'

  const badge = fatal || network.state === 'failed'
    ? { tone: 'error', text: t('badge.error') }
    : active
      ? { tone: 'live', text: t('badge.call', { n: current.participants.length }) }
      : pending
        ? { tone: 'waiting', text: t('badge.connecting') }
        : network.state === 'starting'
          ? { tone: 'waiting', text: t('badge.starting') }
          : { tone: 'waiting', text: t('badge.ready') }

  const notice = fatal || inCall
    ? null
    : network.state === 'failed'
      ? { tone: 'error', text: fail(network.error, safeText(network.message)), retry: true, command: fixCommand(settings, meetError(network.error)) }
      : network.state === 'starting'
        ? { tone: 'plain', text: t('network.starting') }
        : null

  // How many connection relays are listed; outside a call none holds a place.
  const relaysLine = relaySummary(t, network.relays)

  const noticeBox = notice ? (
    <div className={`notice is-${notice.tone}`} role={notice.tone === 'error' ? 'alert' : 'status'}>
      <div className="notice__text">
        <p>{notice.text}</p>
        {notice.command ? <p className="notice__cause">{safeText(meetError(network.error)?.cause)}</p> : null}
      </div>
      {notice.retry || notice.command ? (
        <span className="notice__actions">
          {notice.retry ? <button type="button" className="btn btn--ghost btn--small" onClick={() => call('Reconnect').catch(setLobbyError)}>{t('network.retry')}</button> : null}
          {notice.command ? <CopyButton label={t('command.copy')} onCopy={() => copyText(notice.command)} className="btn btn--ghost btn--small" /> : null}
        </span>
      ) : null}
    </div>
  ) : null

  const tone = stateTone(badge, network, fatal, pending)
  const line = { text: networkLine(t, network.mode, badge.text), tone }
  const entry = inCall ? { text: badge.text, tone } : null
  const canStart = canStartCall({ ready, named, inCall })

  return (
    <I18n.Provider value={i18n}>
      <div className="app">
        <div className="titlebar" />
        <div className="body">
          <Sidebar
            entry={entry}
            name={safeText(settings?.displayName)}
            collapsed={collapsed || narrow}
            forced={narrow}
            inert={dialog === 'settings'}
            canStart={canStart}
            page={dialog === 'settings' ? 'settings' : ''}
            pageButtons={pageButtons}
            network={line}
            onToggle={toggleSidebar}
            onChoose={choose}
            onPage={openPage}
          />
          <div className="main">
            <div className="bar">
              <div className="bar__text">
                <h2 className="bar__title">{t(barTitleKey(view))}</h2>
                <p className="bar__sub">
                  <span className={`dot is-${tone}`} aria-hidden="true" />
                  <span>{badge.text}</span>
                </p>
              </div>
              {view === 'call' ? (
                <div className="bar__actions">
                  <button type="button" className="pill" aria-haspopup="dialog" onClick={openPeople}>{t('call.people')}</button>
                  {current.role === 'host' ? <button type="button" className="pill" onClick={() => setDialog('invite')}>{t('call.invite')}</button> : null}
                </div>
              ) : null}
            </div>

            {view === 'call' ? (
              <CallView
                call={current}
                relays={network.relays}
                leaving={leaving}
                leaveError={leaveError}
                onToggle={toggle}
                onLeave={leave}
                onInvite={() => setDialog('invite')}
                onSecurity={() => setDialog('security')}
                onHelp={(device) => setDialog(`help-${device}`)}
              />
            ) : null}
            {view === 'lobby' ? (
              <Lobby
                intent={intent}
                settings={settings}
                devices={devices}
                lobby={lobby}
                asked={Boolean(prefs.cameraAsked)}
                ready={ready}
                busy={busy}
                error={lobbyError}
                notice={noticeBox}
                relays={relaysLine}
                access={access}
                asking={asking}
                onAsk={askMicrophone}
                onCancelAsk={() => call('CancelMicrophoneRequest').catch(() => {})}
                onToggle={toggle}
                onDevices={(ids) => saveDevices(ids).catch(setLobbyError)}
                onRefresh={refreshDevices}
                onBegin={begin}
                onBack={closeLobby}
                onChangeInvite={(ev) => openJoin(ev.currentTarget)}
                onHelp={(device) => setDialog(`help-${device}`)}
              />
            ) : null}
            {view === 'fatal' ? <Fatal error={fatal} /> : null}
            {view === 'name' ? <NameForm onSave={saveName} /> : null}
            {view === 'home' ? (
              <Home
                max={settings.maxParticipants}
                ready={ready}
                notice={noticeBox}
                relays={relaysLine}
                history={history}
                onClear={clearHistory}
                onStart={() => openLobby('start')}
                onJoin={(ev) => openJoin(ev.currentTarget)}
              />
            ) : null}
            {view === 'joining' ? <Joining call={current} relays={network.relays} leaving={leaving} leaveError={leaveError} onCancel={leave} /> : null}
            {view === 'ended' ? <Ended call={current} people={people} canRetry={invite !== ''} history={history} onClear={clearHistory} onHome={home} onRetry={retry} onNewInvite={(ev) => otherInvite(ev.currentTarget)} /> : null}

            {dialog === 'settings' && settings ? (
              <Modal page title={t('settings.title')} returnFocus={returnTo} onClose={() => setDialog('')}>
                <SettingsPanel
                  settings={settings}
                  devices={devices}
                  version={info?.version}
                  language={locales.includes(prefs.lang) ? prefs.lang : 'system'}
                  theme={theme}
                  onLanguage={(value) => update({ lang: value })}
                  onTheme={(value) => update({ theme: value })}
                  onName={saveName}
                  onDevices={saveDevices}
                  onRefresh={refreshDevices}
                  onNotch={(value) => call('SetShowInNotch', value).then(setSettings)}
                  network={network}
                  onCopy={copyText}
                />
              </Modal>
            ) : null}
          </div>
        </div>

        {toasts.length > 0 ? (
          <div className="toasts">
            {toasts.map((toast) => <p key={toast.id} className="toast">{t('toast.left', { name: safeText(toast.name) || t('call.unnamed') })}</p>)}
          </div>
        ) : null}

        {dialog === 'history' ? (
          <Modal title={t('history.title')} returnFocus={returnTo} onClose={() => setDialog('')}>
            <HistoryList items={history} />
            {history.length > 0 ? (
              <div className="card__actions">
                <button type="button" className="btn btn--ghost" onClick={clearHistory}>{t('history.clear.label')}</button>
              </div>
            ) : null}
          </Modal>
        ) : null}
        {dialog === 'join' ? (
          <Modal title={t('join.title')} returnFocus={returnTo} onClose={closeJoin}>
            <JoinForm
              value={invite}
              error={joinError}
              onChange={(value) => {
                setInvite(value)
                setJoinError(null)
              }}
              onPaste={() => pasteText().then((text) => {
                setInvite(String(text ?? '').trim())
                setJoinError(null)
              }).catch(setJoinError)}
              onSubmit={submitInvite}
            />
          </Modal>
        ) : null}
        {dialog === 'invite' && active && current.invite ? (
          <Modal title={t('invite.title')} onClose={() => setDialog('')}>
            <InviteDialog invite={current.invite} onCopy={copyInvite} onCopyMessage={copyMessage} onNew={newInvite} />
          </Modal>
        ) : null}
        {dialog === 'people' && active ? (
          <Modal title={t('people.title')} onClose={() => setDialog('')}>
            <PeopleList
              call={current}
              asking={removeAsk}
              removing={removing}
              error={removeError}
              onAsk={(id) => {
                setRemoveError(null)
                setRemoveAsk(id)
              }}
              onConfirm={remove}
              onCancel={() => setRemoveAsk('')}
            />
          </Modal>
        ) : null}
        {dialog === 'security' && active ? (
          <Modal title={t('security.title')} onClose={() => setDialog('')}>
            <SecurityDialog security={current.security} link={current.relayLink} via={current.relayVia} />
          </Modal>
        ) : null}
        {helpDevice ? (
          <Modal title={t(`help.title.${helpDevice}`)} onClose={() => setDialog('')}>
            <DeviceHelp
              device={helpDevice}
              media={media[helpDevice]}
              onRetry={() => {
                setDialog('')
                toggle(helpDevice, true)
              }}
            />
          </Modal>
        ) : null}

        <p className="sr-only" role="status" aria-live="polite">{[spoken, ...toasts.map((toast) => t('toast.left', { name: safeText(toast.name) || t('call.unnamed') }))].join(' ')}</p>
      </div>
    </I18n.Provider>
  )
}

export default App
