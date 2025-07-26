import { mount } from 'svelte';
import './styles/tailwind.css';
import './lib/styles/species-display.css';
import App from './App.svelte';

const app = mount(App, {
  target: document.getElementById('app'),
});

export default app;
