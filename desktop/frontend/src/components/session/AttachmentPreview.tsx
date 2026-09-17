import { useEffect, useState } from 'react'
import type { Attachment, Transport } from '../../api/types'
import { reasonOf } from '../../lib/format'
import Button from '../../ui/button'
import Dialog from '../../ui/dialog'
import { fileSize, previewKind } from './bundleModel'
import './inspector.css'

/*
 * One attachment, shown rather than named.
 *
 * A support engineer opening a bundle wants to see the screenshot the
 * customer sent, not read its file name. The bytes come through the
 * Transport: a URL under the attachment route in the browser, a data URL in
 * the desktop app, which is the one thing WKWebView will load from a page
 * on a custom scheme.
 *
 * Nothing here can run: the route serves every attachment with `nosniff`
 * and a sandbox policy, and an HTML or SVG file arrives as `text/plain`, so
 * it is drawn in the text pane rather than as a document.
 */

export interface AttachmentPreviewProps {
  attachment: Attachment
  transport: Transport
  workspaceId: string
  runId: string
  onClose: () => void
  /** Reveals the bundle directory in the desktop's file manager, when the shell can. */
  onOpenFolder?: () => void
}

export default function AttachmentPreview({
  attachment,
  transport,
  workspaceId,
  runId,
  onClose,
  onOpenFolder,
}: AttachmentPreviewProps): JSX.Element {
  const [url, setUrl] = useState('')
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const kind = previewKind(attachment.mime, attachment.name)

  useEffect(() => {
    let cancelled = false
    setUrl('')
    setText('')
    setError('')
    transport
      .attachmentURL(workspaceId, runId, attachment.path)
      .then(async (src) => {
        if (cancelled) return
        setUrl(src)
        if (kind !== 'text') return
        // A text attachment is read rather than framed: an <object> of
        // text/plain is a scroll box with no theme and no direction.
        const res = await fetch(src)
        const body = await res.text()
        if (!cancelled) setText(body)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(reasonOf(err))
      })
    return () => {
      cancelled = true
    }
  }, [transport, workspaceId, runId, attachment.path, kind])

  const meta = [attachment.mime.split(';')[0], fileSize(attachment.size)].filter(Boolean).join(' · ')

  return (
    <Dialog
      open
      title={attachment.name}
      onClose={onClose}
      actions={
        onOpenFolder ? (
          <Button variant="pale" onClick={onOpenFolder}>
            Open in Finder
          </Button>
        ) : null
      }
    >
      <div className="si-prev" data-testid="attachment-preview" data-kind={kind}>
        <p className="si-prev__meta">
          <bdi>{meta}</bdi>
        </p>
        {error ? (
          <p className="si-prev__msg" role="alert">
            {error}
          </p>
        ) : !url ? (
          <p className="si-prev__msg">Reading the file…</p>
        ) : kind === 'image' ? (
          <img className="si-prev__img" src={url} alt={attachment.name} />
        ) : kind === 'pdf' ? (
          <object className="si-prev__doc" data={url} type="application/pdf" aria-label={attachment.name}>
            <p className="si-prev__msg">This window cannot draw a PDF. Open the bundle folder to read it.</p>
          </object>
        ) : kind === 'audio' ? (
          // eslint-disable-next-line jsx-a11y/media-has-caption -- a customer's voice note has no track
          <audio className="si-prev__media" src={url} controls />
        ) : kind === 'video' ? (
          // eslint-disable-next-line jsx-a11y/media-has-caption -- a customer's screen recording has no track
          <video className="si-prev__media" src={url} controls />
        ) : kind === 'text' ? (
          <pre className="si-prev__text sd-bidi" dir="auto">
            {text}
          </pre>
        ) : (
          <p className="si-prev__msg">
            Nothing here can draw a <bdi>{attachment.mime.split(';')[0] || 'file of this type'}</bdi>. It is in the bundle folder.
          </p>
        )}
      </div>
    </Dialog>
  )
}
