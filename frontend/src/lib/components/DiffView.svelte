<script>
  import { escapeHTML } from '$lib/utils/markdown.js';
  import { ChevronRight, ChevronDown, FilePenLine, PanelLeft, PanelRight, Columns } from '@lucide/svelte';

  let { filePath, edits } = $props();

  let collapsed = $state(false);
  let viewMode = $state('side-by-side'); // 'side-by-side' | 'unified'

  function toggle() {
    collapsed = !collapsed;
  }

  function cycleViewMode() {
    viewMode = viewMode === 'side-by-side' ? 'unified' : 'side-by-side';
  }

  /**
   * Compute a line-level unified diff between oldText and newText.
   * Returns an array of segments:
   *   { type: 'context', text, oldLine, newLine }
   *   { type: 'ellipsis', count }
   *   { type: 'changed', pairs: [{ oldText, newText?, oldLine, newLine? }] }
   */
  function computeDiff(oldText, newText) {
    const oldLines = (oldText ?? '').split('\n');
    const newLines = (newText ?? '').split('\n');

    const m = oldLines.length;
    const n = newLines.length;

    const dp = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
    for (let i = 1; i <= m; i++) {
      for (let j = 1; j <= n; j++) {
        if (oldLines[i - 1] === newLines[j - 1]) {
          dp[i][j] = dp[i - 1][j - 1] + 1;
        } else {
          dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
        }
      }
    }

    let i = m, j = n;
    const ops = [];

    while (i > 0 || j > 0) {
      if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
        ops.push({ type: 'context', oldLine: i, newLine: j, text: oldLines[i - 1] });
        i--; j--;
      } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
        ops.push({ type: 'added', newLine: j, text: newLines[j - 1] });
        j--;
      } else if (i > 0) {
        ops.push({ type: 'removed', oldLine: i, text: oldLines[i - 1] });
        i--;
      }
    }

    ops.reverse();

    const MAX_CONTEXT = 3;
    const segments = [];
    let removedBuf = [];
    let addedBuf = [];
    let contextBuf = [];

    function flushChangePair() {
      if (removedBuf.length === 0 && addedBuf.length === 0) return;

      const pairs = [];
      const maxLen = Math.max(removedBuf.length, addedBuf.length);
      for (let k = 0; k < maxLen; k++) {
        pairs.push({
          oldText: k < removedBuf.length ? removedBuf[k].text : undefined,
          newText: k < addedBuf.length ? addedBuf[k].text : undefined,
          oldLine: k < removedBuf.length ? removedBuf[k].oldLine : undefined,
          newLine: k < addedBuf.length ? addedBuf[k].newLine : undefined,
        });
      }

      segments.push({ type: 'changed', pairs });
      removedBuf = [];
      addedBuf = [];
    }

    function flushContext() {
      if (contextBuf.length === 0) return;
      if (contextBuf.length > MAX_CONTEXT) {
        const truncated = contextBuf.length - MAX_CONTEXT;
        segments.push({ type: 'ellipsis', count: truncated });
        segments.push(...contextBuf.slice(-MAX_CONTEXT).map(c => ({
          type: 'context',
          text: c.text,
          oldLine: c.oldLine,
          newLine: c.newLine,
        })));
      } else {
        segments.push(...contextBuf.map(c => ({
          type: 'context',
          text: c.text,
          oldLine: c.oldLine,
          newLine: c.newLine,
        })));
      }
      contextBuf = [];
    }

    for (const op of ops) {
      if (op.type === 'context') {
        if (removedBuf.length > 0 || addedBuf.length > 0) {
          flushChangePair();
          contextBuf.push(op);
        } else {
          contextBuf.push(op);
        }
      } else {
        if (contextBuf.length > 0) {
          flushContext();
        }
        if (op.type === 'removed') {
          removedBuf.push(op);
        } else if (op.type === 'added') {
          addedBuf.push(op);
        }
      }
    }

    flushChangePair();
    flushContext();

    return segments;
  }

  function highlight(line) {
    return escapeHTML(line || '');
  }

  function extractHighlightedRange(hlHtml, start, end) {
    let pos = 0;
    let i = 0;
    const len = hlHtml.length;

    while (i < len && pos < start) {
      if (hlHtml[i] === '<') {
        while (i < len && hlHtml[i] !== '>') i++;
        i++;
      } else {
        if (hlHtml.substring(i, i + 5) === '&amp;' || hlHtml.substring(i, i + 4) === '&lt;' || hlHtml.substring(i, i + 4) === '&gt;' || hlHtml.substring(i, i + 6) === '&#39;' || hlHtml.substring(i, i + 6) === '&quot;') {
          const semi = hlHtml.indexOf(';', i);
          if (semi !== -1) i = semi + 1;
          else i++;
        } else {
          i++;
        }
        pos++;
      }
    }
    const rangeStart = i;

    while (i < len && pos < end) {
      if (hlHtml[i] === '<') {
        while (i < len && hlHtml[i] !== '>') i++;
        i++;
      } else {
        if (hlHtml.substring(i, i + 5) === '&amp;' || hlHtml.substring(i, i + 4) === '&lt;' || hlHtml.substring(i, i + 4) === '&gt;' || hlHtml.substring(i, i + 6) === '&#39;' || hlHtml.substring(i, i + 6) === '&quot;') {
          const semi = hlHtml.indexOf(';', i);
          if (semi !== -1) i = semi + 1;
          else i++;
        } else {
          i++;
        }
        pos++;
      }
    }
    const rangeEnd = i;

    const openStack = [];
    let j = 0;
    while (j < rangeEnd) {
      if (hlHtml[j] === '<') {
        if (hlHtml[j + 1] === '/') {
          openStack.pop();
          let endTag = hlHtml.indexOf('>', j);
          j = endTag + 1;
        } else {
          let endTag = hlHtml.indexOf('>', j);
          if (endTag !== -1) {
            const tag = hlHtml.substring(j, endTag + 1);
            if (endTag < rangeStart) {
              openStack.push(tag);
            }
            j = endTag + 1;
          } else break;
        }
      } else {
        j++;
      }
    }

    let result = '';
    for (const tag of openStack) {
      result += tag;
    }
    result += hlHtml.substring(rangeStart, rangeEnd);
    for (let c = openStack.length - 1; c >= 0; c--) {
      const tagName = openStack[c].match(/<(\w+)/);
      if (tagName) result += '</' + tagName[1] + '>';
    }
    return result;
  }

  function renderHighlightedLine(rawLine, changedStart, changedEnd, className) {
    const hl = highlight(rawLine);
    if (changedStart === undefined || changedEnd === undefined || changedStart >= changedEnd) {
      return hl;
    }
    const before = extractHighlightedRange(hl, 0, changedStart);
    const changed = extractHighlightedRange(hl, changedStart, changedEnd);
    const after = extractHighlightedRange(hl, changedEnd, rawLine.length);
    return before + '<span class="' + className + '">' + changed + '</span>' + after;
  }

  function renderOldLine(oldLine, newLine) {
    if (oldLine === undefined) return '';
    if (newLine === undefined) {
      return highlight(oldLine);
    }
    if (oldLine === newLine) return highlight(oldLine);

    let prefixLen = 0;
    const minLen = Math.min(oldLine.length, newLine.length);
    while (prefixLen < minLen && oldLine[prefixLen] === newLine[prefixLen]) {
      prefixLen++;
    }

    let suffixLen = 0;
    while (
      suffixLen < minLen - prefixLen &&
      oldLine[oldLine.length - 1 - suffixLen] === newLine[newLine.length - 1 - suffixLen]
    ) {
      suffixLen++;
    }

    return renderHighlightedLine(oldLine, prefixLen, oldLine.length - (suffixLen || 0), 'diff-del-inline');
  }

  function renderNewLine(oldLine, newLine) {
    if (newLine === undefined) return '';
    if (oldLine === undefined) {
      return highlight(newLine);
    }
    if (oldLine === newLine) return highlight(oldLine);

    let prefixLen = 0;
    const minLen = Math.min(oldLine.length, newLine.length);
    while (prefixLen < minLen && oldLine[prefixLen] === newLine[prefixLen]) {
      prefixLen++;
    }

    let suffixLen = 0;
    while (
      suffixLen < minLen - prefixLen &&
      oldLine[oldLine.length - 1 - suffixLen] === newLine[newLine.length - 1 - suffixLen]
    ) {
      suffixLen++;
    }

    return renderHighlightedLine(newLine, prefixLen, newLine.length - (suffixLen || 0), 'diff-ins-inline');
  }
</script>

<div class="rounded-lg overflow-hidden border border-ctp-surface0 mb-2"
  style="background:color-mix(in srgb, #65b73b 8%, #ffffff)">
  <!-- Header -->
  <div class="flex items-center gap-1 px-2.5 py-1.5 text-xs">
    <button
      class="flex items-center gap-1 cursor-pointer rounded px-1 py-0.5 hover:bg-black/5"
      onclick={toggle}
    >
      <span class="flex items-center">
        {#if collapsed}
          <ChevronRight size={12} />
        {:else}
          <ChevronDown size={12} />
        {/if}
      </span>
      <FilePenLine size={14} class="text-[#65b73b]" />
      <span class="font-semibold" style="color:#65b73b">edit</span>
    </button>
    <span class="text-ctp-overlay0 text-[10px] ml-auto truncate max-w-[300px]" title={filePath}>
      {(filePath ?? '').split('/').slice(-2).join('/')}
    </span>
    <!-- View mode toggle -->
    <button
      class="cursor-pointer rounded px-1 py-0.5 hover:bg-black/10 ml-1"
      onclick={cycleViewMode}
      title={viewMode === 'side-by-side' ? 'Switch to unified view' : 'Switch to side-by-side view'}
    >
      {#if viewMode === 'side-by-side'}
        <Columns size={12} class="text-ctp-overlay0" />
      {:else}
        <PanelLeft size={12} class="text-ctp-overlay0" />
      {/if}
    </button>
  </div>

  <!-- Diff content -->
  <div class="border-t border-ctp-surface0" class:hidden={collapsed}>
    {#if viewMode === 'side-by-side'}
      <!-- Side-by-side view -->
      <div class="text-[11px] font-mono overflow-x-auto">
        {#each (edits ?? []) as edit, ei}
          {#if ei > 0}
            <div class="border-t border-ctp-surface0/50"></div>
          {/if}
          <div class="diff-side-by-side">
            <div class="diff-panel diff-panel-old">
              <div class="diff-panel-header">
                <span class="text-[10px] font-semibold text-ctp-overlay0/80 uppercase tracking-wide">Old</span>
              </div>
              <div class="diff-panel-content">
                {#each computeDiff(edit.oldText, edit.newText) as segment}
                  {#if segment.type === 'ellipsis'}
                    <div class="diff-side-line px-2 py-0.5 text-ctp-overlay0 italic text-[10px] select-none">
                      … {segment.count} lines …
                    </div>
                  {:else if segment.type === 'changed'}
                    {#each segment.pairs as pair}
                      {#if pair.oldText !== undefined}
                        <div class="diff-side-line diff-line-removed flex leading-normal">
                          <span class="diff-line-num w-8 text-right pr-1.5 shrink-0 text-ctp-overlay0/60 select-none">
                            {pair.oldLine}
                          </span>
                          <span class="w-4 shrink-0 select-none text-[#e95f59]">-</span>
                          <span class="flex-1 pr-2 whitespace-pre">
                            {@html renderOldLine(pair.oldText, pair.newText)}
                          </span>
                        </div>
                      {:else}
                        <!-- No old counterpart — blank filler -->
                        <div class="diff-side-line diff-line-empty flex leading-normal">
                          <span class="diff-line-num w-8 text-right pr-1.5 shrink-0 select-none text-ctp-overlay0/30">
                            ·
                          </span>
                          <span class="w-4 shrink-0 select-none"> </span>
                          <span class="flex-1 pr-2 whitespace-pre text-ctp-overlay0/20">
                            
                          </span>
                        </div>
                      {/if}
                    {/each}
                  {:else if segment.type === 'context'}
                    <div class="diff-side-line diff-line-context flex leading-normal">
                      <span class="diff-line-num w-8 text-right pr-1.5 shrink-0 text-ctp-overlay0/60 select-none">
                        {segment.oldLine}
                      </span>
                      <span class="w-4 shrink-0 select-none text-ctp-overlay0/30"> </span>
                      <span class="flex-1 pr-2 whitespace-pre">
                        {@html highlight(segment.text)}
                      </span>
                    </div>
                  {/if}
                {/each}
              </div>
            </div>
            <div class="diff-divider"></div>
            <div class="diff-panel diff-panel-new">
              <div class="diff-panel-header">
                <span class="text-[10px] font-semibold text-ctp-overlay0/80 uppercase tracking-wide">New</span>
              </div>
              <div class="diff-panel-content">
                {#each computeDiff(edit.oldText, edit.newText) as segment}
                  {#if segment.type === 'ellipsis'}
                    <div class="diff-side-line px-2 py-0.5 text-ctp-overlay0 italic text-[10px] select-none">
                      … {segment.count} lines …
                    </div>
                  {:else if segment.type === 'changed'}
                    {#each segment.pairs as pair}
                      {#if pair.newText !== undefined}
                        <div class="diff-side-line diff-line-added flex leading-normal">
                          <span class="diff-line-num w-8 text-right pr-1.5 shrink-0 text-ctp-overlay0/60 select-none">
                            {pair.newLine}
                          </span>
                          <span class="w-4 shrink-0 select-none text-[#65b73b]">+</span>
                          <span class="flex-1 pr-2 whitespace-pre">
                            {@html renderNewLine(pair.oldText, pair.newText)}
                          </span>
                        </div>
                      {:else}
                        <!-- No new counterpart — blank filler -->
                        <div class="diff-side-line diff-line-empty flex leading-normal">
                          <span class="diff-line-num w-8 text-right pr-1.5 shrink-0 select-none text-ctp-overlay0/30">
                            ·
                          </span>
                          <span class="w-4 shrink-0 select-none"> </span>
                          <span class="flex-1 pr-2 whitespace-pre text-ctp-overlay0/20">
                            
                          </span>
                        </div>
                      {/if}
                    {/each}
                  {:else if segment.type === 'context'}
                    <div class="diff-side-line diff-line-context flex leading-normal">
                      <span class="diff-line-num w-8 text-right pr-1.5 shrink-0 text-ctp-overlay0/60 select-none">
                        {segment.newLine}
                      </span>
                      <span class="w-4 shrink-0 select-none text-ctp-overlay0/30"> </span>
                      <span class="flex-1 pr-2 whitespace-pre">
                        {@html highlight(segment.text)}
                      </span>
                    </div>
                  {/if}
                {/each}
              </div>
            </div>
          </div>
        {/each}
      </div>
    {:else}
      <!-- Unified view (original) -->
      <div class="text-[11px] font-mono overflow-x-auto" style="background:color-mix(in srgb, #ffffff 50%, #ffffff);">
        {#each (edits ?? []) as edit, ei}
          {#if ei > 0}
            <div class="border-t border-ctp-surface0/50"></div>
          {/if}
          <div class="diff-block">
            {#each computeDiff(edit.oldText, edit.newText) as segment}
              {#if segment.type === 'ellipsis'}
                <div class="px-3 py-0.5 text-ctp-overlay0 italic text-[10px] select-none">
                  … {segment.count} unchanged lines …
                </div>
              {:else if segment.type === 'changed'}
                {#each segment.pairs as pair}
                  {#if pair.oldText !== undefined && pair.newText !== undefined}
                    <div class="diff-line diff-line-removed flex leading-normal">
                      <span class="diff-line-num w-10 text-right pr-2 shrink-0 text-ctp-overlay0/60 select-none">
                        {pair.oldLine}
                      </span>
                      <span class="w-5 shrink-0 select-none text-[#e95f59]">-</span>
                      <span class="flex-1 pr-3 whitespace-pre">
                        {@html renderOldLine(pair.oldText, pair.newText)}
                      </span>
                    </div>
                    <div class="diff-line diff-line-added flex leading-normal">
                      <span class="diff-line-num w-10 text-right pr-2 shrink-0 text-ctp-overlay0/60 select-none">
                        {pair.newLine}
                      </span>
                      <span class="w-5 shrink-0 select-none text-[#65b73b]">+</span>
                      <span class="flex-1 pr-3 whitespace-pre">
                        {@html renderNewLine(pair.oldText, pair.newText)}
                      </span>
                    </div>
                  {:else if pair.oldText !== undefined}
                    <div class="diff-line diff-line-removed flex leading-normal">
                      <span class="diff-line-num w-10 text-right pr-2 shrink-0 text-ctp-overlay0/60 select-none">
                        {pair.oldLine}
                      </span>
                      <span class="w-5 shrink-0 select-none text-[#e95f59]">-</span>
                      <span class="flex-1 pr-3 whitespace-pre">
                        {@html highlight(pair.oldText)}
                      </span>
                    </div>
                  {:else}
                    <div class="diff-line diff-line-added flex leading-normal">
                      <span class="diff-line-num w-10 text-right pr-2 shrink-0 text-ctp-overlay0/60 select-none">
                        {pair.newLine}
                      </span>
                      <span class="w-5 shrink-0 select-none text-[#65b73b]">+</span>
                      <span class="flex-1 pr-3 whitespace-pre">
                        {@html highlight(pair.newText)}
                      </span>
                    </div>
                  {/if}
                {/each}
              {:else if segment.type === 'context'}
                <div class="diff-line diff-line-context flex leading-normal">
                  <span class="diff-line-num w-10 text-right pr-2 shrink-0 text-ctp-overlay0/60 select-none">
                    {segment.oldLine}
                  </span>
                  <span class="w-5 shrink-0 select-none text-ctp-overlay0/40"> </span>
                  <span class="flex-1 pr-3 whitespace-pre">
                    {@html highlight(segment.text)}
                  </span>
                </div>
              {/if}
            {/each}
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .diff-del-inline {
    background: color-mix(in srgb, #e95f59 25%, transparent);
    text-decoration: none;
  }
  .diff-ins-inline {
    background: color-mix(in srgb, #65b73b 25%, transparent);
  }

  /* Side-by-side layout */
  .diff-side-by-side {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    background: color-mix(in srgb, #ffffff 50%, #ffffff);
  }

  .diff-panel {
    min-width: 0;
  }

  .diff-panel-header {
    padding: 4px 8px 2px;
    border-bottom: 1px solid color-mix(in srgb, #000 8%, transparent);
    font-size: 10px;
  }

  .diff-panel-content {
    overflow-x: auto;
  }

  .diff-divider {
    width: 1px;
    background: color-mix(in srgb, #000 10%, transparent);
    align-self: stretch;
  }

  .diff-side-line {
    padding-left: 8px;
    padding-right: 8px;
  }

  .diff-line-removed {
    background: color-mix(in srgb, #e95f59 12%, transparent);
  }

  .diff-line-added {
    background: color-mix(in srgb, #65b73b 12%, transparent);
  }

  .diff-line-context {
    background: transparent;
  }

  .diff-line-empty {
    background: color-mix(in srgb, #000 3%, transparent);
  }
</style>
