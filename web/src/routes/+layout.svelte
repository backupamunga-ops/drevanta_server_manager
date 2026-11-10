<script lang="ts">
        import '../app.css';

        let { children } = $props();
</script>

<div class="shell">
        <header>
                <div class="brand">
                        <span class="brand-mark" aria-hidden="true"></span>
                        <span class="brand-name">Drevanta</span>
                        <span class="brand-sub">server console</span>
                </div>
                <div class="conn">loopback-only · SSH tunnel</div>
        </header>

        <main>
                {@render children()}
        </main>

        <footer>
                <span>Drevanta · private server console</span>
                <span>loopback-only · SSH tunnel required</span>
        </footer>
</div>

<style>
        .shell {
                max-width: 1080px;
                margin: 0 auto;
                padding: 20px 24px 40px;
                min-height: 100vh;
                display: flex;
                flex-direction: column;
        }

        header {
                display: flex;
                align-items: baseline;
                justify-content: space-between;
                margin-bottom: 24px;
                border-bottom: 1px solid var(--border);
                padding-bottom: 16px;
        }

        .brand {
                display: flex;
                align-items: baseline;
                gap: 10px;
        }

        .brand-mark {
                width: 10px;
                height: 10px;
                border-radius: 3px;
                background: var(--accent);
                display: inline-block;
                box-shadow: 0 0 8px var(--accent);
        }

        .brand-name {
                font-size: 20px;
                font-weight: 700;
                letter-spacing: 0.02em;
        }

        .brand-sub {
                color: var(--text-dim);
                font-size: 13px;
        }

        .conn {
                color: var(--text-dim);
                font-size: 12px;
        }

        main {
                flex: 1;
        }

        footer {
                display: flex;
                justify-content: space-between;
                gap: 12px;
                margin-top: 32px;
                padding-top: 16px;
                border-top: 1px solid var(--border);
                color: var(--text-dim);
                font-size: 12px;
        }
</style>
