/**
 * A screen as panes that fill the window under the header: the intro on the page itself, and a
 * second pane beside it (under it in a narrow window). `optional` hides the second pane when narrow.
 */
export function Split({ intro, side, form = false, optional = false, label }) {
  return (
    <div className={`split${side ? '' : ' split--solo'}${optional ? ' split--optional' : ''}`}>
      <main className="pane pane--intro" data-pane>
        <div className="pane__inner">{intro}</div>
      </main>
      {side ? (
        <aside className={`pane pane--side${form ? ' pane--form' : ''}`} aria-label={label} data-pane>
          {form ? <div className="pane__inner">{side}</div> : side}
        </aside>
      ) : null}
    </div>
  )
}
