dev:
	pnpm --filter @nusashell/example-mail run build
	pnpm --filter @nusashell/desktop run dev

test:
	pnpm -r test