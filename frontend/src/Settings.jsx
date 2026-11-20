import { useState } from 'react'
import { useI18n } from './context.js'
import { safeText } from './format.js'
import { languageNames, locales } from './i18n.js'
import { relayProblems, relaySummary } from './relays.js'
import { networkCommand, networkCommandParts, networkSources } from './command.js'
import { CopyButton, DeviceSelect, PathText } from './parts.jsx'

/** One list of the network, read only: its entries and where they came from, or "none". */
function NetworkFact({ label, parts }) {
  const { t } = useI18n()
  return (
    <div className="fact fact--wide">
      <dt className="label">{label}</dt>
      {parts.length === 0 ? <dd className="fact__value">{t('settings.network.none')}</dd> : null}
      {parts.map((part) => (
        <dd key={part.from} className="fact__value fact__list">
          {part.list.map((item) => safeText(item)).join(', ')}
          {part.from ? <span className="fact__from"> {t('settings.network.from', { source: part.from })}</span> : null}
        </dd>
      ))}
    </div>
  )
}

/**
 * What the network uses now, read only: the network (public, your own or this computer only) and the connection
 * relays are set with flags when the app starts, and the command to start it again is ready to copy.
 */
function NetworkInfo({ settings, network, onCopy }) {
  const { t, fail } = useI18n()
  const { mode, nodes, relays } = networkSources(settings)
  const summary = relaySummary(t, network?.relays)
  const problems = relayProblems(network?.relays)
  const command = networkCommand(settings)
  // One flag per line, each line but the last continued with a backslash, so a pasted selection runs as one command.
  const shown = networkCommandParts(settings).join(' \\\n  ')
  return (
    <>
      <dl className="facts facts--network">
        <div className="fact fact--wide">
          <dt className="label">{t('settings.network.mode')}</dt>
          <dd className="fact__value">{t(`settings.network.${mode}`)}</dd>
        </div>
        {mode === 'public' ? null : <NetworkFact label={t('settings.nodes')} parts={nodes} />}
        <NetworkFact label={t('settings.relays')} parts={relays ? [relays] : []} />
      </dl>
      {summary ? <p className="field-note">{summary} {problems.map((s) => fail(s.error)).join(' ')}</p> : null}
      <p className="field-note">{t(`settings.network.${mode}.note`)}</p>
      <p className="field-note">{t('settings.network.how')}</p>
      <div className="command">
        <code className="command__text">{shown}</code>
        <CopyButton label={t('command.copy')} onCopy={() => onCopy(command)} className="btn btn--ghost" />
      </div>
    </>
  )
}

/** The settings as a page of three columns: you, the devices, the network. */
export function SettingsPanel({ settings, devices, network, version, language, theme, onLanguage, onTheme, onName, onDevices, onRefresh, onNotch, onCopy }) {
  const { t, fail } = useI18n()
  const [name, setName] = useState(settings?.displayName ?? '')
  const [error, setError] = useState(null)
  const [nameError, setNameError] = useState(null)
  const [said, setSaid] = useState(false)
  const pick = (patch) => onDevices({ cameraId: settings.cameraId, microphoneId: settings.microphoneId, speakerId: settings.speakerId, ...patch }).catch(setError)

  const saveName = (ev) => {
    ev.preventDefault()
    setNameError(null)
    setSaid(false)
    onName(name)
      .then((next) => {
        setName(next?.displayName ?? name)
        setSaid(true)
      })
      .catch(setNameError)
  }

  return (
    <div className="settings">
      <section className="settings__group" aria-labelledby="settings-you">
        <h3 className="settings__title" id="settings-you">{t('settings.group.you')}</h3>
        <form onSubmit={saveName}>
          <label className="label" htmlFor="settings-name">{t('settings.name')}</label>
          <div className="form__row">
            <input
              id="settings-name"
              className="field"
              value={name}
              onChange={(ev) => setName(ev.target.value)}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={nameError ? 'true' : undefined}
              aria-describedby="settings-name-note"
            />
            <button type="submit" className="btn btn--ghost" id="settings-save-name">{t('settings.name.save')}</button>
          </div>
          {nameError ? <p className="alert alert--plain field-note" role="alert">{fail(nameError)}</p> : null}
          <p className="field-note" id="settings-name-note" role="status">{said ? t('settings.name.saved') : t('settings.name.note')}</p>
        </form>
        <div className="settings__grid">
          <div>
            <label className="label" htmlFor="settings-language">{t('settings.language')}</label>
            <select id="settings-language" className="field" value={language} onChange={(ev) => onLanguage(ev.target.value)}>
              <option value="system">{t('settings.language.system')}</option>
              {locales.map((code) => (
                <option key={code} value={code}>{languageNames[code]}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="label" htmlFor="settings-theme">{t('settings.theme')}</label>
            <select id="settings-theme" className="field" value={theme} onChange={(ev) => onTheme(ev.target.value)}>
              <option value="system">{t('theme.system')}</option>
              <option value="light">{t('theme.light')}</option>
              <option value="dark">{t('theme.dark')}</option>
            </select>
          </div>
        </div>
      </section>

      <section className="settings__group" aria-labelledby="settings-devices">
        <h3 className="settings__title" id="settings-devices">{t('settings.group.devices')}</h3>
        <DeviceSelect id="settings-camera" label={t('device.camera')} list={devices.cameras} value={settings?.cameraId ?? ''} onChange={(id) => pick({ cameraId: id })} />
        <DeviceSelect id="settings-microphone" label={t('device.microphone')} list={devices.microphones} value={settings?.microphoneId ?? ''} onChange={(id) => pick({ microphoneId: id })} />
        <DeviceSelect id="settings-speaker" label={t('device.speaker')} list={devices.speakers} value={settings?.speakerId ?? ''} onChange={(id) => pick({ speakerId: id })} />
        <div className="settings__row">
          <p className="field-note">{t('settings.speaker.note')}</p>
          <button type="button" className="link" onClick={onRefresh}>{t('lobby.refresh')}</button>
        </div>
        <div>
          <label className="check">
            <input type="checkbox" checked={settings?.showInNotch ?? true} onChange={(ev) => onNotch(ev.target.checked).catch(setError)} aria-describedby="settings-notch-note" />
            <span>{t('settings.notch')}</span>
          </label>
          <p className="field-note" id="settings-notch-note">{t('settings.notch.note')}</p>
        </div>
        {error ? <p className="alert alert--plain field-note" role="alert">{fail(error)}</p> : null}
      </section>

      <section className="settings__group" aria-labelledby="settings-network">
        <h3 className="settings__title" id="settings-network">{t('settings.group.network')}</h3>
        <NetworkInfo settings={settings} network={network} onCopy={onCopy} />
        <h3 className="settings__title settings__title--next" id="settings-about">{t('settings.group.about')}</h3>
        <dl className="facts facts--about">
          <div className="fact fact--wide">
            <dt className="label">{t('settings.dataDir')}</dt>
            <dd><PathText value={settings?.dataDir} empty={t('settings.dataDir.none')} /></dd>
          </div>
        </dl>
        {version ? <p className="note">{t('settings.version', { v: safeText(version) })}</p> : null}
      </section>
    </div>
  )
}
