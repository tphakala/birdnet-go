import{c as ae}from"./index.js";import{p as oe,a as re,s as w,b as ne,z as ie,n as s,k as W,W as se,o as u,l as S,m as d,t as le,q as ce,r as de,h as F,j as me,g as t,$ as pe,d as fe}from"./vendor.js";function ue(m,h){(m.key==="Enter"||m.key===" ")&&(m.preventDefault(),h())}var he=W(`<style>/* Custom binoculars cursor - black outline for light theme */
    .cursor-binoculars {
      cursor:
        url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='24' height='24' viewBox='0 0 24 24' fill='none' stroke='black' stroke-width='1.5'%3E%3Ccircle cx='8' cy='14' r='3.5'/%3E%3Ccircle cx='16' cy='14' r='3.5'/%3E%3Cpath d='M8 11V8h8v3'/%3E%3C/svg%3E")
          12 12,
        crosshair;
    }
    /* Custom binoculars cursor - white outline for dark theme */
    [data-theme='dark'] .cursor-binoculars {
      cursor:
        url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='24' height='24' viewBox='0 0 24 24' fill='none' stroke='white' stroke-width='1.5'%3E%3Ccircle cx='8' cy='14' r='3.5'/%3E%3Ccircle cx='16' cy='14' r='3.5'/%3E%3Cpath d='M8 11V8h8v3'/%3E%3C/svg%3E")
          12 12,
        crosshair;
    }
    /* Touch device support */
    @media (hover: none) {
      .cursor-binoculars {
        cursor: default;
      }
      .game-bird::before {
        content: '👁';
        position: absolute;
        top: -1.5rem;
        left: 50%;
        transform: translateX(-50%);
        font-size: 1.5rem;
        opacity: 0.7;
      }
    }

    @keyframes fly {
      0% {
        transform: translate(0, 0) rotate(0deg);
      }
      25% {
        transform: translate(10px, -10px) rotate(5deg);
      }
      50% {
        transform: translate(0, -15px) rotate(0deg);
      }
      75% {
        transform: translate(-10px, -10px) rotate(-5deg);
      }
      100% {
        transform: translate(0, 0) rotate(0deg);
      }
    }
    @keyframes gentle-pulse {
      0%,
      100% {
        opacity: 1;
      }
      50% {
        opacity: 0.7;
      }
    }
    @keyframes feather-fall {
      0% {
        transform: translate(-50%, 0) rotate(0deg);
        opacity: 1;
      }
      100% {
        transform: translate(calc(-50% + 30px), 100px) rotate(45deg);
        opacity: 0;
      }
    }
    .bird {
      cursor: pointer;
      user-select: none;
      transition: all 0.3s ease;
      position: relative;
      display: inline-block;
    }
    .bird:hover {
      transform: scale(1.1);
      filter: brightness(1.2);
      animation: gentle-bounce 1s infinite;
    }
    .bird::after {
      content: '👀';
      position: absolute;
      top: -1.5em;
      left: 50%;
      transform: translateX(-50%) translateY(10px);
      font-size: 0.4em;
      opacity: 0;
      transition: all 0.3s ease;
      pointer-events: none;
      white-space: nowrap;
    }
    .bird:hover::after {
      opacity: 1;
      transform: translateX(-50%) translateY(0);
    }
    @keyframes gentle-bounce {
      0%,
      100% {
        transform: translateY(0) scale(1.1);
      }
      50% {
        transform: translateY(-3px) scale(1.1);
      }
    }
    .bird.flying {
      animation: gentle-pulse 2s infinite;
      transform: scale(1.1);
    }
    .bird.caught {
      transform: scale(1.2);
      filter: brightness(1.2);
    }
    #game-container {
      position: fixed;
      inset: 0;
      pointer-events: none;
      z-index: 50;
    }
    .game-bird {
      position: absolute;
      font-size: 2rem;
      cursor: pointer;
      pointer-events: auto;
      transition: all 0.2s ease;
      animation: fly 1s infinite;
      touch-action: manipulation;
    }
    .game-bird.spotted {
      animation: none;
      filter: brightness(1.2) sepia(0.3);
    }
    .score-display {
      position: fixed;
      top: 1rem;
      right: 1rem;
      padding: 0.5rem 1rem;
      border-radius: 0.5rem;
      font-weight: bold;
      opacity: 0;
      transition: opacity 0.3s ease;
      display: flex;
      flex-direction: column;
      gap: 0.5rem;
      min-width: 200px;
      text-align: right;
    }
    .score-display.visible {
      opacity: 1;
    }
    .high-score {
      font-size: 0.875rem;
      opacity: 0.8;
    }
    .timer {
      font-size: 0.875rem;
      color: var(--fallback-p, oklch(var(--p) / 1));
    }
    /* Spotting effect */
    .spotting-ring {
      position: absolute;
      border: 2px solid currentColor;
      border-radius: 50%;
      pointer-events: none;
      opacity: 0;
      transform: translate(-50%, -50%) scale(0.5);
      animation: spotting-ring 0.5s ease-out forwards;
    }
    @keyframes spotting-ring {
      0% {
        opacity: 1;
        width: 30px;
        height: 30px;
      }
      100% {
        opacity: 0;
        width: 60px;
        height: 60px;
      }
    }
    @media (hover: none) {
      .bird:hover {
        animation: none;
        transform: none;
      }
      .bird::after {
        display: none;
      }
    }
    .feather {
      position: absolute;
      left: 50%;
      top: 100%;
      font-size: 0.4em;
      pointer-events: none;
      animation: feather-fall 3s ease-in-out forwards;
    }</style>`),ge=W('<div><div class="text-center p-8 rounded-lg bg-base-100 shadow-lg"><h1 class="text-6xl font-bold text-base-content mb-4">4<span class="bird" role="button" tabindex="0">0</span>4</h1> <h2 class="text-3xl font-semibold text-base-content/70 mb-8">Page Not Found</h2> <a href="/" class="btn btn-primary normal-case text-base font-semibold hover:bg-primary-focus transition duration-300">Go to Dashboard</a></div> <div id="game-container"></div> <div class="score-display bg-base-100 shadow-lg text-base-content"><div class="current-score">Birds Spotted: 0</div> <div class="timer">Time: 0:00</div> <div class="high-score">Best: 0 birds in 0:00</div></div></div>');function ye(m,h){oe(h,!0);let j=re(h,"className",3,""),l,p,g,v,b,y;const M=["🐦","🦜","🦢","🦆","🦅","🦉"],I=600,q=1500,_=2e3,K=4e3,L=typeof window<"u"&&("ontouchstart"in window||navigator.maxTouchPoints>0);let n=w(!1),r=w(0),k=w(0),f,c,i=w(ne({score:0,time:0}));function D(e){const o=Math.floor(e/1e3),a=Math.floor(o/60),x=o%60;return`${a}:${x.toString().padStart(2,"0")}`}function V(){if(!t(n))return;const e=Date.now()-t(k);b&&(b.textContent=`Time: ${D(e)}`)}function z(){v&&(v.textContent=`Birds Spotted: ${t(r)}`)}function Z(){const e=Date.now()-t(k);(t(r)>t(i).score||t(r)===t(i).score&&e<t(i).time)&&(s(i,{score:t(r),time:e},!0),typeof localStorage<"u"&&(localStorage.setItem("birdSpotterHighScore",t(r).toString()),localStorage.setItem("birdSpotterHighTime",e.toString()))),y&&(y.textContent=`Best: ${t(i).score} birds in ${D(t(i).time)}`)}function J(e,o){const a=document.createElement("div");a.className="spotting-ring text-base-content",a.style.left=e+"px",a.style.top=o+"px",p.appendChild(a),setTimeout(()=>a.remove(),500)}function E(){if(t(n))return;const e=document.createElement("div");e.className="feather",e.textContent=["🪶","❃","❋"][Math.floor(Math.random()*3)],l.appendChild(e),setTimeout(()=>e.remove(),3e3)}function O(){s(n,!0),s(r,0),s(k,Date.now(),!0),z(),g.classList.add("visible"),l.classList.add("flying"),c&&clearInterval(c),l.querySelectorAll(".feather").forEach(e=>e.remove()),L||document.body.classList.add("cursor-binoculars"),f=setInterval(V,1e3),N()}function Q(){s(n,!1),l.classList.remove("flying"),g.classList.remove("visible"),document.body.classList.remove("cursor-binoculars"),f&&clearInterval(f),c=setInterval(()=>{Math.random()<.3&&E()},4e3),Array.from(p.children).forEach(e=>{e.classList.contains("spotting-ring")||e.remove()})}function N(){if(!t(n))return;const e=document.createElement("div");e.className="game-bird"+(L?"":" cursor-binoculars"),e.textContent=M[Math.floor(Math.random()*M.length)],e.style.left=Math.random()*(window.innerWidth-50)+"px",e.style.top=Math.random()*(window.innerHeight-50)+"px";const o=a=>{if(e.classList.contains("spotted"))return;s(r,t(r)+1),z(),e.classList.add("spotted");const x=a,A=a,ee=x.touches?.[0]?.clientX||A.clientX||0,te=x.touches?.[0]?.clientY||A.clientY||0;J(ee,te),setTimeout(()=>{e.style.opacity="0",setTimeout(()=>e.remove(),300)},500),Z()};e.addEventListener("click",o),e.addEventListener("touchstart",o),p.appendChild(e),setTimeout(()=>{if(e.parentNode&&!e.classList.contains("spotted")){const a=Math.random()>.5?1:-1;e.style.transform=`translateX(${a*window.innerWidth}px)`,e.style.opacity="0",setTimeout(()=>e.parentNode?.removeChild(e),1e3)}},_+Math.random()*(K-_)),setTimeout(N,I+Math.random()*(q-I))}function B(){t(n)||O()}function $(e){e.key==="Escape"&&t(n)&&Q()}ie(()=>(typeof localStorage<"u"&&s(i,{score:parseInt(localStorage.getItem("birdSpotterHighScore")||"0"),time:parseInt(localStorage.getItem("birdSpotterHighTime")||"0")},!0),c=setInterval(()=>{Math.random()<.3&&E()},4e3),setTimeout(E,1e3),document.addEventListener("keydown",$),()=>{f&&clearInterval(f),c&&clearInterval(c),document.removeEventListener("keydown",$)}));var C=ge();se(e=>{var o=he();pe.title="404 - Page Not Found",F(e,o)});var H=S(C),R=S(H),T=u(S(R));T.__click=B,T.__keydown=[ue,B],d(T,e=>l=e,()=>l);var X=u(H,2);d(X,e=>p=e,()=>p);var Y=u(X,2),G=S(Y);d(G,e=>v=e,()=>v);var P=u(G,2);d(P,e=>b=e,()=>b);var U=u(P,2);d(U,e=>y=e,()=>y),d(Y,e=>g=e,()=>g),le(e=>de(C,1,e),[()=>ce(ae("min-h-screen bg-base-200 flex items-center justify-center",j()))]),F(m,C),me()}fe(["click","keydown"]);export{ye as default};
