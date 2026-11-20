import { useI18n } from './context.js'
import { detectOS } from './platform.js'
import { meetError } from './errors.js'

/** Whether a device state is a refusal of the system, which the person fixes in the system's settings. */
export function isDenied(media) {
  return media?.state === 'error' && meetError(media.error)?.kind === 'permission'
}

/** Where to allow the camera or the microphone on this system. The sentence of the service names no path. */
export function useFix(device) {
  const { t } = useI18n()
  return t(`help.${detectOS(window.navigator)}.${device}`)
}

/** The body of the dialog behind "How to fix": the sentence of the error, then the steps on this system. */
export function DeviceHelp({ device, media, onRetry }) {
  const { t, fail } = useI18n()
  const fix = useFix(device)
  const why = media?.error ?? media?.hint
  return (
    <div className="help">
      {why ? <p className="alert alert--plain" role="alert">{fail(why)}</p> : null}
      <p>{fix}</p>
      <div className="form__actions">
        <button type="button" className="btn btn--wide" onClick={onRetry}>{t('help.retry')}</button>
      </div>
    </div>
  )
}
