// The e-mail view: a form component that shows a mail a mail task read
// (ADR-draft-mail-html-view).
//
// A mail task's get answers an envelope into a variable — sender, recipients, subject,
// date, attachments, the text, and with includeBody="html" the HTML body, cleaned on the
// server. This component shows that variable to the person working the task. It names
// the variable by its key, like any field, so the server lets a task's holder read it;
// but it is not keyed for form-js, so the form never submits it back: completing the
// task does not rewrite a mail into the log.
//
// The HTML body is somebody else's markup. It is rendered in a sandboxed frame — no
// scripts, no same origin, no forms; links open in a new tab — and the frame's own
// Content-Security-Policy loads nothing from elsewhere until the person asks for the
// images. A remote image in a mail is how a sender learns when, and from where, it was
// opened; here it would tell them every time somebody opens the task.

import { useContext, useState } from 'preact/hooks';
import { FormContext, Label } from '@bpmn-io/form-js-viewer';

export const MAIL_VIEW_TYPE = 'mailview';

const STRINGS = {
  de: {
    from: 'Von', to: 'An', cc: 'Cc', date: 'Empfangen', attachments: 'Anhänge',
    html: 'HTML', text: 'Text', views: 'Ansicht',
    empty: 'Keine E-Mail in «{key}».',
    noBody: 'Die E-Mail wurde ohne ihren Inhalt gelesen.',
    blocked: 'Externe Bilder sind blockiert, damit der Absender nicht erfährt, wann und von wo diese E-Mail geöffnet wird.',
    loadImages: 'Bilder laden',
    frame: 'Inhalt der E-Mail',
    textCut: 'Der Text ist gekürzt.',
    htmlCut: 'Die HTML-Fassung ist gekürzt.'
  },
  en: {
    from: 'From', to: 'To', cc: 'Cc', date: 'Received', attachments: 'Attachments',
    html: 'HTML', text: 'Text', views: 'View',
    empty: 'No e-mail in “{key}”.',
    noBody: 'The e-mail was read without its content.',
    blocked: 'Remote images are blocked, so the sender does not learn when and from where this e-mail is opened.',
    loadImages: 'Load images',
    frame: 'Content of the e-mail',
    textCut: 'The text is shortened.',
    htmlCut: 'The HTML version is shortened.'
  }
};

// stringsFor answers the component's words in the page's language: the `atlasLocale`
// a page passes, German otherwise, as the Tasks app is.
export function stringsFor(locale) {
  return STRINGS[locale] || STRINGS.de;
}

// REMOTE_RE finds a reference the frame would fetch from elsewhere: an image, a
// background, a CSS url().
const REMOTE_RE = /(?:\bsrc|\bbackground|\bposter)\s*=\s*["']?\s*(?:https?:)?\/\/|url\(\s*["']?\s*(?:https?:)?\/\//i;

export function hasRemoteContent(html) {
  return REMOTE_RE.test(html || '');
}

// framed is the document the frame shows: the cleaned HTML behind a policy that loads
// nothing from elsewhere — unless the person asked for the images — and a referrer
// policy that never tells an image host which page it was opened from. Put first, the
// policy is in force for everything the mail brings.
export function framed(html, images) {
  const csp = images
    ? "default-src 'none'; style-src 'unsafe-inline'; img-src data: https: http:; font-src data:"
    : "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:";
  return `<meta http-equiv="Content-Security-Policy" content="${csp}">` +
    '<meta name="referrer" content="no-referrer"><base target="_blank">' + (html || '');
}

// readMail is the variable the view names, wherever the form's data holds it: its key,
// below the paths of the groups around it.
export function readMail(form, pathRegistry, field, indexes) {
  const key = (field.key || '').trim();
  if (!form || !key) return null;
  let path = key.split('.');
  if (pathRegistry && typeof pathRegistry.getValuePath === 'function') {
    path = pathRegistry.getValuePath(field, { replacements: { [field.id]: key }, indexes }) || path;
  }
  let v = form._getState().data;
  for (const seg of path) {
    if (v === null || typeof v !== 'object') return null;
    v = v[seg];
  }
  if (typeof v === 'string') return v.trim() ? { body: v } : null;
  return v && typeof v === 'object' && !Array.isArray(v) ? v : null;
}

const list = (v) => (Array.isArray(v) ? v.filter((x) => typeof x === 'string' && x) : []);
const text = (v) => (typeof v === 'string' ? v : '');

function fmtDate(iso, locale) {
  const d = new Date(iso);
  if (!iso || Number.isNaN(d.getTime())) return '';
  return d.toLocaleString(locale === 'en' ? 'en-GB' : 'de-CH', { dateStyle: 'medium', timeStyle: 'short' });
}

function fmtSize(n) {
  const v = Number(n);
  if (!Number.isFinite(v) || v < 0) return '';
  if (v < 1024) return `${v} B`;
  if (v < 1024 * 1024) return `${Math.round(v / 1024)} KB`;
  return `${(v / 1024 / 1024).toFixed(1)} MB`;
}

function MailFrame(props) {
  const { html, t } = props;
  const [ images, setImages ] = useState(false);
  const remote = hasRemoteContent(html);
  return (
    <div class="atlas-mailview-html">
      { remote && !images
        ? (
          <div class="atlas-mailview-blocked" role="note">
            <span>{ t.blocked }</span>
            <button type="button" class="atlas-mailview-load" onClick={ () => setImages(true) }>{ t.loadImages }</button>
          </div>
        )
        : null }
      <div class="atlas-mailview-framebox">
        <iframe
          class="atlas-mailview-frame"
          title={ t.frame }
          sandbox="allow-popups allow-popups-to-escape-sandbox"
          referrerpolicy="no-referrer"
          srcdoc={ framed(html, images) } />
      </div>
    </div>
  );
}

function Head(props) {
  const { mail, t, locale } = props;
  const from = text(mail.from);
  const fromName = text(mail.fromName);
  const rows = [
    [ t.from, fromName && from ? `${fromName} <${from}>` : (fromName || from) ],
    [ t.to, list(mail.to).join(', ') ],
    [ t.cc, list(mail.cc).join(', ') ],
    [ t.date, fmtDate(text(mail.receivedAt), locale) ]
  ].filter((r) => r[1]);
  const atts = Array.isArray(mail.attachments) ? mail.attachments.filter((a) => a && typeof a === 'object') : [];
  return (
    <div class="atlas-mailview-head">
      { text(mail.subject) ? <div class="atlas-mailview-subject">{ text(mail.subject) }</div> : null }
      <dl>
        { rows.map(([ k, v ]) => [ <dt key={ k + '-k' }>{ k }</dt>, <dd key={ k + '-v' }>{ v }</dd> ]) }
        { atts.length
          ? [
            <dt key="att-k">{ t.attachments }</dt>,
            <dd key="att-v" class="atlas-mailview-atts">
              { atts.map((a, i) => (
                <span key={ i } class="atlas-mailview-att">{ text(a.name) || 'attachment' }{ fmtSize(a.size) ? ` · ${fmtSize(a.size)}` : '' }</span>
              )) }
            </dd>
          ]
          : null }
      </dl>
    </div>
  );
}

export function MailView(props) {
  const { field, domId, indexes } = props;
  const { getService } = useContext(FormContext);
  const form = getService('form', false);
  const locale = getService('atlasLocale', false) || 'de';
  const t = stringsFor(locale);
  const mail = readMail(form, getService('pathRegistry', false), field, indexes);
  const html = mail ? text(mail.html) : '';
  const body = mail ? text(mail.body) : '';
  const [ view, setView ] = useState(null);
  const shown = view || (html ? 'html' : 'text');

  let content;
  if (!mail) {
    content = <div class="atlas-mailview-empty">{ t.empty.replace('{key}', field.key || '') }</div>;
  } else {
    const hasHTML = !!html.trim();
    const hasText = !!body.trim();
    content = (
      <div class="atlas-mailview-card">
        <Head mail={ mail } t={ t } locale={ locale } />
        { hasHTML && hasText
          ? (
            <div class="atlas-mailview-tabs" role="group" aria-label={ t.views }>
              <button type="button" aria-pressed={ shown === 'html' ? 'true' : 'false' } onClick={ () => setView('html') }>{ t.html }</button>
              <button type="button" aria-pressed={ shown === 'text' ? 'true' : 'false' } onClick={ () => setView('text') }>{ t.text }</button>
            </div>
          )
          : null }
        { hasHTML && shown === 'html'
          ? <MailFrame html={ html } t={ t } />
          : hasText
            ? <pre class="atlas-mailview-text">{ body }</pre>
            : <div class="atlas-mailview-empty">{ t.noBody }</div> }
        { (hasHTML && shown === 'html' && mail.htmlTruncated === true) ? <div class="atlas-mailview-cut">{ t.htmlCut }</div> : null }
        { (shown === 'text' || !hasHTML) && mail.bodyTruncated === true ? <div class="atlas-mailview-cut">{ t.textCut }</div> : null }
      </div>
    );
  }

  return (
    <div class="fjs-form-field atlas-mailview" id={ domId }>
      { field.label ? <Label label={ field.label } /> : null }
      { content }
    </div>
  );
}

function MailViewIcon(props) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="54" height="54" viewBox="0 0 54 54" fill="none" { ...props }>
      <rect x="10" y="15" width="34" height="24" rx="2" stroke="currentColor" stroke-width="2" />
      <path d="M11 17l16 12 16-12" stroke="currentColor" stroke-width="2" stroke-linejoin="round" />
    </svg>
  );
}

MailView.config = {
  type: MAIL_VIEW_TYPE,
  // Not keyed: form-js submits keyed fields only, and a mail is shown, never answered.
  keyed: false,
  name: 'E-mail view',
  group: 'presentation',
  icon: MailViewIcon,
  create: (options = {}) => ({ label: 'E-Mail', key: 'mail', ...options })
};
