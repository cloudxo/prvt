<div class="grid grid-cols-2 gap-4">
    {#each themes as t}
        <div class="cursor-pointer flex px-4 py-2 text-sm bg-shade-neutral rounded shadow hover:bg-shade-100" on:click={() => setTheme(t)}>
            <span class="color-circle theme-{t}" title="Theme: {t}" aria-hidden="true"></span>
            <span class="ml-2">{t}</span>
        </div>
    {/each}
</div>

<style>
.color-circle {
    display: inline-block;
    width: 1.2em;
    height: 1.2em;

    box-shadow: 
        0 0 0 0.075em #edf2f7,
        0 0 0 0.15em #4a5568;
    border-radius: 50%;
    background-size:
        50% 100%,
        50% 100%;
    background-repeat: no-repeat;
    background-image:
        var(--picker-gradient-left),
        var(--picker-gradient-right);
    background-position: left top, right top;
}
</style>

<script>
// Theme data
import {themes} from '../lib/theme'

// Stores
import {modal} from '../stores'

// Set the theme
function setTheme(t) {
    // Set the theme by telling the service worker
    navigator.serviceWorker.controller.postMessage({
        message: 'set-theme',
        theme: t
    })
    
    // Hide the modal
    $modal = null
}
</script>
