import { mountProxy } from './app';
import './proxy.css';

const root = document.getElementById('proxy-app');
if (root) mountProxy(root);
