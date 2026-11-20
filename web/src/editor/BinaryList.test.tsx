import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Item } from '../api/types';
import { BINARY_PREVIEW_MAX_BYTES } from '../config';
import { binaryUrl, humanSize, previewKind } from './binary';
import { BinaryList } from './BinaryList';
import { DataView } from './DataView';

const bin = (name: string, mime: string, size: number, ref = `blob://e1/${name}`) => ({ ref, mime, name, size });
const item = (binary: Record<string, unknown>): Item => ({ json: { ok: true }, binary });

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => { fetchMock = vi.fn(); vi.stubGlobal('fetch', fetchMock); });
afterEach(() => vi.unstubAllGlobals());

describe('binary helpers', () => {
  it('classifies MIME types and refuses SVG and HTML-like script carriers as images', () => {
    expect(previewKind('image/png')).toBe('image');
    expect(previewKind('image/svg+xml')).toBe('none');
    expect(previewKind('audio/mpeg')).toBe('audio');
    expect(previewKind('video/mp4')).toBe('video');
    expect(previewKind('application/pdf')).toBe('pdf');
    expect(previewKind('text/csv; charset=utf-8')).toBe('text');
    expect(previewKind('application/json')).toBe('text');
    expect(previewKind('application/vnd.api+json')).toBe('text');
    expect(previewKind('application/zip')).toBe('none');
  });
  it('formats sizes', () => {
    expect(humanSize(0)).toBe('0 B');
    expect(humanSize(1536)).toBe('1.5 KB');
    expect(humanSize(5 * 1024 * 1024)).toBe('5.0 MB');
  });
  it('builds download URLs from a full reference or a bare id', () => {
    expect(binaryUrl('blob://e1/b2', undefined)).toBe('/api/v1/executions/e1/binary/b2');
    expect(binaryUrl('b2', 'e9', true)).toBe('/api/v1/executions/e9/binary/b2?inline=true');
    expect(binaryUrl('b2', undefined)).toBeNull();
  });
});

describe('BinaryList', () => {
  it('shows name, type, size and a download link', () => {
    render(<BinaryList items={[item({ data: bin('report.zip', 'application/zip', 2048) })]} executionId="e1" />);
    expect(screen.getByText('report.zip')).toBeInTheDocument();
    expect(screen.getByText('application/zip')).toBeInTheDocument();
    expect(screen.getByText('2.0 KB')).toBeInTheDocument();
    const link = screen.getByRole('link', { name: 'Download report.zip' });
    expect(link).toHaveAttribute('href', '/api/v1/executions/e1/binary/report.zip');
    expect(link).toHaveAttribute('download', 'report.zip');
    expect(document.querySelector('img,audio,video,pre')).toBeNull();
  });

  it('previews images, audio and video from same-origin URLs', () => {
    render(<BinaryList items={[item({ a: bin('p.png', 'image/png', 10), b: bin('s.mp3', 'audio/mpeg', 10), c: bin('v.mp4', 'video/mp4', 10) })]} executionId="e1" />);
    expect(screen.getByRole('img', { name: 'p.png' })).toHaveAttribute('src', '/api/v1/executions/e1/binary/p.png?inline=true');
    expect(screen.getByLabelText('s.mp3').tagName).toBe('AUDIO');
    expect(screen.getByLabelText('v.mp4').tagName).toBe('VIDEO');
  });

  it('offers only a download for PDFs and SVG', () => {
    render(<BinaryList items={[item({ a: bin('d.pdf', 'application/pdf', 10), b: bin('i.svg', 'image/svg+xml', 10) })]} executionId="e1" />);
    expect(screen.getByText(/PDF files are not previewed/)).toBeInTheDocument();
    expect(document.querySelector('img,iframe,embed,object')).toBeNull();
  });

  it('loads text on request and renders it as text, not markup', async () => {
    fetchMock.mockResolvedValue(new Response('<b>hi</b>\na,b', { status: 200 }));
    render(<BinaryList items={[item({ a: bin('n.csv', 'text/csv', 14) })]} executionId="e1" />);
    expect(fetchMock).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: 'Show preview' }));
    expect(await screen.findByText(/<b>hi<\/b>/)).toBeInTheDocument();
    expect(document.querySelector('pre b')).toBeNull();
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/executions/e1/binary/n.csv?inline=true');
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ credentials: 'same-origin' });
  });

  it('does not fetch text above the size cap', () => {
    render(<BinaryList items={[item({ a: bin('big.txt', 'text/plain', BINARY_PREVIEW_MAX_BYTES + 1) })]} executionId="e1" />);
    expect(screen.getByText(/Too large to preview/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Show preview' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Download big.txt' })).toBeInTheDocument();
  });

  it('truncates a body that is bigger than the cap when the size was unknown', async () => {
    fetchMock.mockResolvedValue(new Response('x'.repeat(BINARY_PREVIEW_MAX_BYTES + 10), { status: 200 }));
    render(<BinaryList items={[item({ a: bin('u.txt', 'text/plain', 0) })]} executionId="e1" />);
    await userEvent.click(screen.getByRole('button', { name: 'Show preview' }));
    await waitFor(() => expect(screen.getByText(/Showing the first/)).toBeInTheDocument());
  });

  it('reports a failed preview', async () => {
    fetchMock.mockResolvedValue(new Response('', { status: 404, statusText: 'Not Found' }));
    render(<BinaryList items={[item({ a: bin('u.txt', 'text/plain', 3) })]} executionId="e1" />);
    await userEvent.click(screen.getByRole('button', { name: 'Show preview' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('404');
  });

  it('renders nothing for items without binary data and appears inside DataView', () => {
    const { container } = render(<BinaryList items={[{ json: {} }]} executionId="e1" />);
    expect(container).toBeEmptyDOMElement();
    render(<DataView items={[item({ f: bin('x.zip', 'application/zip', 1) })]} label="Output" executionId="e1" />);
    expect(screen.getByRole('list', { name: 'Binary data' })).toBeInTheDocument();
  });
});
