import { useState } from 'react'
import { useI18n } from './context.js'
import { Avatar } from './Avatar.jsx'
import { useFrames, useLevelBar } from './media.js'
import { DeviceSelect } from './parts.jsx'
import { Icon } from './Icons.jsx'
import { isDenied, useFix } from './DeviceHelp.jsx'

/** The self-view. A closed camera shows the initial, a refused or missing one says why and what to do. */
function Preview({ camera, name, asked, onToggle }) {
  const { t, fail } = useI18n()
  const on = camera.state === 'on'
  const img = useFrames('self', on)
  const fix = useFix('camera')
  const failed = camera.state === 'error'
  return (
    <div className={`preview${failed ? ' is-error' : ''}`} role="group" aria-label={t('lobby.preview')}>
      {on ? <img ref={img} className="preview__video is-mirrored" alt="" /> : null}
      {failed ? (
        <>
          <p className="alert alert--plain" role="alert">{fail(camera.error)}</p>
          {isDenied(camera) ? <p className="preview__note">{fix}</p> : null}
          <p className="preview__note">{t('lobby.without')}</p>
          <div className="preview__actions">
            <button type="button" className="btn btn--ghost btn--small" onClick={() => onToggle(true)}>{t('help.retry')}</button>
          </div>
        </>
      ) : on ? null : (
        <>
          <Avatar className="avatar" name={name} seed={name} />
          <p className="preview__text">{t('lobby.preview.off')}</p>
          {asked ? null : <p className="preview__note">{t('lobby.preview.ask')}</p>}
        </>
      )}
    </div>
  )
}

/** The level of the microphone while it is on. */
function Meter({ microphone }) {
  const { t } = useI18n()
  const on = microphone.state === 'on'
  const bar = useLevelBar('self', on)
  return (
    <div className="meter">
      <div className="meter__track" role="img" aria-label={t('lobby.meter')}>
        <div className="meter__fill" ref={bar} />
      </div>
      <p className="meter__text">{on ? t('lobby.meter') : microphone.state === 'error' ? t('device.microphone') : t('lobby.meter.off')}</p>
    </div>
  )
}

/**
 * Before a call: the self-view, the microphone meter, the three device pickers and the name, then
 * the one button that starts or joins. Nothing here opens a device by itself.
 */
export function Lobby({ intent, settings, devices, lobby, asked, ready, busy, error, onToggle, onDevices, onRefresh, onBegin, onBack, onChangeInvite, onHelp, notice, relays, access, asking, onAsk, onCancelAsk }) {
  const { t, fail } = useI18n()
  const [name, setName] = useState(settings.displayName ?? '')
  const pick = (patch) => onDevices({ cameraId: settings.cameraId, microphoneId: settings.microphoneId, speakerId: settings.speakerId, ...patch })
  const micOn = lobby.microphone.state === 'on'
  // On but only silence: the system may be refusing the microphone without saying so.
  const hint = micOn ? lobby.microphone.hint : null
  const camOn = lobby.camera.state === 'on'
  return (
    <section className="lobby" aria-labelledby="lobby-title">
      <div className="lobby__stage" data-pane>
        <Preview camera={lobby.camera} name={name} asked={asked} onToggle={(on) => onToggle('camera', on)} />
        <div className="devicebar">
          <button
            type="button"
            className={`round${micOn ? ' is-on' : ''}${lobby.microphone.state === 'error' ? ' is-error' : ''}`}
            aria-label={t(micOn ? 'device.microphone.off' : 'device.microphone.on')}
            title={t(micOn ? 'device.microphone.off' : 'device.microphone.on')}
            onClick={() => onToggle('microphone', !micOn)}
          >
            <Icon name="mic" off={!micOn} />
          </button>
          <Meter microphone={lobby.microphone} />
          <button
            type="button"
            className={`round${camOn ? ' is-on' : ''}${lobby.camera.state === 'error' ? ' is-error' : ''}`}
            aria-label={t(camOn ? 'device.camera.off' : 'device.camera.on')}
            title={t(camOn ? 'device.camera.off' : 'device.camera.on')}
            onClick={() => onToggle('camera', !camOn)}
          >
            <Icon name="camera" off={!camOn} />
          </button>
        </div>
        {lobby.microphone.state === 'off' && access === 'not-determined' ? (
          <div className="lobby__problem" role="status">
            <p className="note">{t(asking ? 'lobby.mic.asking' : 'lobby.mic.ask')}</p>
            {asking ? (
              <button type="button" className="btn btn--ghost btn--small" onClick={onCancelAsk}>{t('joining.cancel')}</button>
            ) : (
              <button type="button" className="btn btn--ghost btn--small" onClick={onAsk}>{t('help.title.microphone')}</button>
            )}
          </div>
        ) : null}
        {lobby.microphone.state === 'off' && (access === 'denied' || access === 'restricted') ? (
          <div className="lobby__problem" role="alert">
            <p className="alert alert--plain">{t(`error.microphone-${access}`)}</p>
            {access === 'denied' ? <button type="button" className="btn btn--ghost btn--small" onClick={() => onHelp('microphone')}>{t('help.open')}</button> : null}
          </div>
        ) : null}
        {lobby.microphone.state === 'error' || hint ? (
          <div className="lobby__problem" role={hint ? 'status' : 'alert'}>
            <p className={hint ? 'note' : 'alert alert--plain'}>{fail(hint ?? lobby.microphone.error)}</p>
            {hint || isDenied(lobby.microphone) ? <button type="button" className="btn btn--ghost btn--small" onClick={() => onHelp('microphone')}>{t('help.open')}</button> : null}
          </div>
        ) : null}
      </div>

      <div className="lobby__side" data-pane>
        <form
          className="lobby__form"
          onSubmit={(ev) => {
            ev.preventDefault()
            onBegin(name)
          }}
        >
          <h1 className="lobby__title" id="lobby-title">{t(`lobby.title.${intent}`)}</h1>
          {notice}
          <div>
            <label className="label" htmlFor="lobby-name">{t('name.label')}</label>
            <input id="lobby-name" className="field" value={name} onChange={(ev) => setName(ev.target.value)} autoComplete="off" spellCheck={false} />
          </div>
          <DeviceSelect id="lobby-camera" label={t('device.camera')} list={devices.cameras} value={settings.cameraId} onChange={(id) => pick({ cameraId: id })} />
          <DeviceSelect id="lobby-microphone" label={t('device.microphone')} list={devices.microphones} value={settings.microphoneId} onChange={(id) => pick({ microphoneId: id })} />
          <DeviceSelect id="lobby-speaker" label={t('device.speaker')} list={devices.speakers} value={settings.speakerId} onChange={(id) => pick({ speakerId: id })} />
          {error ? <p className="alert alert--plain" role="alert">{fail(error)}</p> : null}
          {intent === 'join' ? <p className="note">{t('join.ready')}</p> : null}
          {relays ? <p className="note">{relays}</p> : null}
          <div className="lobby__actions">
            <button type="button" className="btn btn--ghost" onClick={onBack}>{t('nav.back')}</button>
            <button type="submit" className="btn" disabled={busy || !ready}>{t(intent === 'join' ? 'lobby.join' : 'lobby.start')}</button>
          </div>
          <div className="lobby__links">
            <button type="button" className="link" onClick={onRefresh}>{t('lobby.refresh')}</button>
            {intent === 'join' ? <button type="button" className="link" onClick={onChangeInvite}>{t('join.change')}</button> : null}
          </div>
        </form>
      </div>
    </section>
  )
}
