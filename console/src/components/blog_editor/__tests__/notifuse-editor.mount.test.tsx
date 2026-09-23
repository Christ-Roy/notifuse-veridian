/**
 * NotifuseEditor — test de montage réel de Tiptap.
 *
 * POURQUOI CE FICHIER EXISTE (Veridian, 2026-09-23)
 * Le bump de sécurité @tiptap/core 3.20.0 → 3.31.3 (GHSA-j95f-988m-3j2f,
 * GHSA-cp6q-959q-f8rh) touche 68 fichiers de `console/src/components/blog_editor`
 * qui n'avaient AUCUN test. Les seuls signaux disponibles étaient `tsc` et le
 * build Vite : tous deux vérifient la surface de TYPES, aucun n'EXÉCUTE Tiptap.
 *
 * Et l'éditeur n'est monté que par `PostDrawer`, à l'intérieur d'un Drawer antd
 * fermé par défaut — donc `pages.smoke.test.tsx`, qui rend pourtant `BlogPage`,
 * ne l'instancie pas non plus. Côté E2E, le test « Navigation → Blog » de la
 * suite console échoue en amont, dans l'authentification (`waitForURL` qui
 * expire dans `helpers.ts`), et n'atteint donc jamais la page.
 *
 * Autrement dit : avant ce fichier, un build vert ne disait RIEN de l'éditeur.
 * Ce test ferme le trou en exerçant le chemin de code qui a changé — parsing du
 * HTML d'entrée par le schéma, `mergeAttributes` sur les attributs de bloc,
 * sérialisation en HTML et en JSON.
 */
import { describe, it, expect, vi } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { createRef } from 'react'
import { NotifuseEditor, type NotifuseEditorRef } from '../NotifuseEditor'

// ResizeObserver et scrollIntoView n'existent pas sous jsdom ; Tiptap et les
// barres d'outils flottantes les appellent au montage.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub
Element.prototype.scrollIntoView = vi.fn()

const CONTENU = '<h2>Titre Veridian</h2><p>Bonjour <strong>monde</strong></p>'

describe('NotifuseEditor — Tiptap est réellement instancié', () => {
  it('monte sans lever et expose une zone contenteditable ProseMirror', async () => {
    const ref = createRef<NotifuseEditorRef>()
    const { container } = render(<NotifuseEditor ref={ref} initialContent={CONTENU} />)

    await waitFor(() => {
      expect(container.querySelector('.ProseMirror')).not.toBeNull()
    })
    expect(container.querySelector('[contenteditable="true"]')).not.toBeNull()
  })

  it('parse le HTML fourni et le resérialise (schéma + mergeAttributes)', async () => {
    const ref = createRef<NotifuseEditorRef>()
    render(<NotifuseEditor ref={ref} initialContent={CONTENU} />)

    await waitFor(() => expect(ref.current).not.toBeNull())
    await waitFor(() => expect(ref.current!.getHTML()).toContain('Bonjour'))

    const html = ref.current!.getHTML()
    // Le contenu traverse le schéma Tiptap : la structure doit survivre.
    expect(html).toContain('<h2')
    expect(html).toContain('<strong>monde</strong>')

    const json = ref.current!.getJSON()
    expect(json).not.toBeNull()
    expect(JSON.stringify(json)).toContain('Titre Veridian')
  })

  it('setContent remplace le document et getJSON le reflète', async () => {
    const ref = createRef<NotifuseEditorRef>()
    render(<NotifuseEditor ref={ref} initialContent={CONTENU} />)

    await waitFor(() => expect(ref.current).not.toBeNull())
    await waitFor(() => expect(ref.current!.getHTML()).toContain('Bonjour'))

    ref.current!.setContent('<p>Contenu remplacé</p>')

    await waitFor(() => {
      expect(ref.current!.getHTML()).toContain('Contenu remplacé')
    })
    // Contrôle négatif : l'ancien contenu a bien disparu. Sans lui, un
    // setContent qui ne ferait rien du tout passerait le test précédent.
    expect(ref.current!.getHTML()).not.toContain('Titre Veridian')
  })
})
