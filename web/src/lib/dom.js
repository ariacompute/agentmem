// Shared, dependency-free DOM helpers for both views.

export function el(tag, className, text) {
  const node = document.createElement(tag)
  if (className) node.className = className
  if (text !== undefined && text !== null) node.textContent = String(text)
  return node
}

export function section(title) {
  const box = el('section', 'card')
  box.appendChild(el('h2', 'card-title', title))
  return box
}

/** A two-column key/value line used by the health card and decision list. */
export function kvRow(key, value, mono = false) {
  const row = el('div', 'kv')
  row.appendChild(el('b', null, key))
  row.appendChild(el('span', mono ? 'mono' : null, value))
  return row
}

export function codeBlock(text) {
  return el('pre', 'code mono', text)
}

export function chip(text, extra = '') {
  return el('span', `chip ${extra}`.trim(), text)
}

export function button(text, className, onClick) {
  const node = el('button', className, text)
  node.type = 'button'
  if (typeof onClick === 'function') node.addEventListener('click', onClick)
  return node
}
