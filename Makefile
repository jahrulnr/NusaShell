dev:
	rm -rf apps/desktop/.vite node_modules/.vite node_modules/.cache
	pnpm --filter @nusashell/example-mail run build
	pnpm --filter @nusashell/desktop run dev

test:
	pnpm -r test