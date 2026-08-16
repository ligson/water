<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as THREE from 'three'

const riverbedStonesSrc = new URL('../../assets/auth/riverbed-stones.jpg', import.meta.url).href
const kohakuKoiSrc = new URL('../../assets/auth/fish-koi-kohaku-right.png', import.meta.url).href
const showaKoiSrc = new URL('../../assets/auth/fish-koi-showa-left.png', import.meta.url).href
const yamabukiKoiSrc = new URL('../../assets/auth/fish-koi-yamabuki-right.png', import.meta.url).href
const shiroKoiSrc = new URL('../../assets/auth/fish-koi-shiro-left.png', import.meta.url).href

type WaterThemeName = 'ruoshui' | 'qingci' | 'xuanzhi' | 'zhusha' | 'xuanmo'

type WaterThemePalette = {
  deep: string
  mid: string
  glow: string
  light: string
}

type FishMotionConfig = {
  radius: number
  strength: number
}

type FishMotionState = {
  offset: THREE.Vector2
  velocity: THREE.Vector2
  escapeDirection: THREE.Vector2
  effort: number
  startleTime: number
  startleStrength: number
  threatLatched: boolean
  swimClock: number
}

const props = defineProps<{
  theme: string
}>()

const rootRef = ref<HTMLElement | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)

const themePalettes: Record<WaterThemeName, WaterThemePalette> = {
  ruoshui: { deep: '#073746', mid: '#16717a', glow: '#72d7cf', light: '#d8fff8' },
  qingci: { deep: '#173f3c', mid: '#3c8175', glow: '#a9e3d2', light: '#ecfff6' },
  xuanzhi: { deep: '#302c24', mid: '#75684d', glow: '#d3c09a', light: '#fff1cf' },
  zhusha: { deep: '#48201d', mid: '#9b4a3b', glow: '#edac94', light: '#ffe2d6' },
  xuanmo: { deep: '#0b2021', mid: '#285755', glow: '#72b9b7', light: '#d6f5ef' },
}

const palette = computed(
  () => themePalettes[(props.theme as WaterThemeName) ?? 'ruoshui'] ?? themePalettes.ruoshui,
)
const sceneStyle = computed(() => ({
  '--water-auth-deep': palette.value.deep,
  '--water-auth-mid': palette.value.mid,
  '--water-auth-stones': `url("${riverbedStonesSrc}")`,
}))

let renderer: THREE.WebGLRenderer | undefined
let scene: THREE.Scene | undefined
let camera: THREE.OrthographicCamera | undefined
let waterPlane: THREE.Mesh<THREE.PlaneGeometry, THREE.ShaderMaterial> | undefined
let stonesTexture: THREE.Texture | undefined
let kohakuKoiTexture: THREE.Texture | undefined
let showaKoiTexture: THREE.Texture | undefined
let yamabukiKoiTexture: THREE.Texture | undefined
let shiroKoiTexture: THREE.Texture | undefined
let frameId: number | undefined
let resizeObserver: ResizeObserver | undefined
let destroyed = false
let rootWidth = 0
let rootHeight = 0
let interactionPulse = 0
let pointerEnergy = 0
let pointerActive = false
let fishSeed = 0
let lastFrameTime = 0

const pointer = new THREE.Vector2(0.5, 0.5)
const pointerTarget = new THREE.Vector2(0.5, 0.5)
const pulseOrigin = new THREE.Vector2(0.5, 0.5)
const fishMotionConfigs: FishMotionConfig[] = [
  { radius: 0.16, strength: 0.045 },
  { radius: 0.17, strength: 0.05 },
  { radius: 0.18, strength: 0.06 },
  { radius: 0.15, strength: 0.04 },
]
const fishPhaseOffsets = [0, 2.1, 4.3, 6.7]
const fishMotionStates: FishMotionState[] = fishMotionConfigs.map((_, index) => ({
  offset: new THREE.Vector2(),
  velocity: new THREE.Vector2(),
  escapeDirection: new THREE.Vector2(0, index % 2 === 0 ? 1 : -1),
  effort: 0,
  startleTime: 0,
  startleStrength: 0,
  threatLatched: false,
  swimClock: 0,
}))
const fishCenters = fishMotionConfigs.map(() => new THREE.Vector2())
const fishMotionCenter = new THREE.Vector2()
const fishMotionDelta = new THREE.Vector2()

const vertexShader = `
  varying vec2 vUv;

  void main() {
    vUv = uv;
    gl_Position = vec4(position, 1.0);
  }
`

const fragmentShader = `
  uniform sampler2D uStones;
  uniform sampler2D uKohakuKoi;
  uniform sampler2D uShowaKoi;
  uniform sampler2D uYamabukiKoi;
  uniform sampler2D uShiroKoi;
  uniform vec2 uResolution;
  uniform vec2 uTextureResolution;
  uniform vec2 uPointer;
  uniform vec2 uPulseOrigin;
  uniform float uPointerEnergy;
  uniform float uPulse;
  uniform float uTime;
  uniform float uFishSeed;
  uniform vec4 uFishEffort;
  uniform vec4 uFishSwimClock;
  uniform vec2 uFishOffset1;
  uniform vec2 uFishOffset2;
  uniform vec2 uFishOffset3;
  uniform vec2 uFishOffset4;
  uniform vec2 uFishEscape1;
  uniform vec2 uFishEscape2;
  uniform vec2 uFishEscape3;
  uniform vec2 uFishEscape4;
  uniform vec3 uDeep;
  uniform vec3 uMid;
  uniform vec3 uGlow;
  uniform vec3 uLight;
  varying vec2 vUv;

  vec2 coverUv(vec2 uv) {
    float screenAspect = uResolution.x / max(uResolution.y, 1.0);
    float imageAspect = uTextureResolution.x / max(uTextureResolution.y, 1.0);

    if (screenAspect > imageAspect) {
      uv.y = (uv.y - 0.5) * (imageAspect / screenAspect) + 0.5;
    } else {
      uv.x = (uv.x - 0.5) * (screenAspect / imageAspect) + 0.5;
    }
    return uv;
  }

  float waveField(vec2 uv) {
    float aspect = uResolution.x / max(uResolution.y, 1.0);
    vec2 p = (uv - 0.5) * vec2(aspect, 1.0);
    float t = uTime;
    float h = sin(dot(p, vec2(8.5, 2.9)) + t * 0.72) * 0.48;
    h += sin(dot(p, vec2(-5.1, 9.4)) - t * 0.57) * 0.31;
    h += sin(dot(p, vec2(13.2, -4.6)) + t * 0.91) * 0.15;
    h += sin(dot(p, vec2(21.0, 7.2)) - t * 1.18) * 0.06;
    return h;
  }

  float rippleField(vec2 uv, vec2 origin, float strength, float frequency, float speed) {
    vec2 delta = uv - origin;
    delta.x *= uResolution.x / max(uResolution.y, 1.0);
    float distanceToOrigin = length(delta);
    float ring = sin(distanceToOrigin * frequency - uTime * speed);
    return ring * exp(-distanceToOrigin * 16.0) * strength;
  }

  float surfaceHeight(vec2 uv) {
    float base = waveField(uv) * 0.055;
    float wake = rippleField(uv, uPointer, uPointerEnergy * 0.085, 52.0, 4.2);
    float pulse = rippleField(uv, uPulseOrigin, uPulse * 0.22, 66.0, 6.4);
    return base + wake + pulse;
  }

  float causticField(vec2 p) {
    float t = uTime;
    float a = sin(p.x * 15.0 + sin(p.y * 8.0 - t * 0.54) + t * 0.68);
    float b = sin(p.y * 14.0 + sin(p.x * 7.0 + t * 0.43) - t * 0.62);
    float primary = pow(1.0 - abs(a), 28.0) * 0.55;
    primary += pow(1.0 - abs(b), 28.0) * 0.45;

    float c = sin((p.x + p.y) * 10.5 - t * 0.46);
    float d = sin((p.x - p.y) * 12.0 + t * 0.51);
    float secondary = pow(1.0 - abs(c), 30.0) * 0.52;
    secondary += pow(1.0 - abs(d), 30.0) * 0.48;
    return primary * 0.68 + secondary * 0.32;
  }

  vec2 fishSpriteUv(
    vec2 uv,
    vec2 center,
    float angle,
    vec2 size,
    float phase,
    float swimClock,
    float effort,
    float turnBend,
    float headDirection
  ) {
    float aspect = uResolution.x / max(uResolution.y, 1.0);
    vec2 local = (uv - center) * vec2(aspect, 1.0);
    float cosine = cos(angle);
    float sine = sin(angle);
    local = mat2(cosine, -sine, sine, cosine) * local;

    float longitudinal = local.x / size.x;
    float tailProgress = clamp(0.5 - headDirection * longitudinal, 0.0, 1.0);
    float tailBend = smoothstep(0.12, 1.0, tailProgress);
    float motionVariation = 0.5 + 0.5 * sin(phase * 1.731 + 0.7);
    float tailAmount = mix(0.1, 0.24, motionVariation) * mix(1.0, 1.32, smoothstep(0.12, 0.9, effort));
    float swimPhase = swimClock + phase + headDirection * longitudinal * 3.1;
    float swim = sin(swimPhase);
    float tailWeight = tailBend * tailBend;
    float bodyWave = smoothstep(0.06, 0.94, tailProgress);
    local.y += swim * size.y * (0.006 + bodyWave * 0.035 + tailWeight * tailAmount);
    local.y += turnBend * size.y * (bodyWave * 0.1 + tailWeight * 0.46);
    local.x += cos(swimPhase * 0.9 + motionVariation) * size.x * tailWeight * mix(0.028, 0.07, motionVariation);
    return local / size + 0.5;
  }

  vec2 safeNormalize(vec2 value) {
    float len = length(value);
    if (len < 0.0001) {
      return vec2(0.0, 1.0);
    }
    return value / len;
  }

  float safeAngle(vec2 value, float fallback) {
    if (length(value) < 0.0001) {
      return fallback;
    }
    return atan(value.y, value.x);
  }

  vec2 turnResponse(float from, float to, float amount) {
    float delta = atan(sin(to - from), cos(to - from));
    float response = smoothstep(0.08, 0.9, amount);
    float heading = from + clamp(delta, -0.28, 0.28) * response;
    float bend = clamp(delta / 1.2, -1.0, 1.0) * response;
    return vec2(heading, bend);
  }

  float fishWake(vec2 uv, vec2 center, float aspect, float falloff, float intensity) {
    vec2 delta = uv - center;
    delta.x *= aspect;
    return exp(-length(delta) * falloff) * intensity;
  }

  float fishAura(vec2 uv, vec2 center, float angle, vec2 size) {
    float aspect = uResolution.x / max(uResolution.y, 1.0);
    vec2 local = (uv - center) * vec2(aspect, 1.0);
    float cosine = cos(angle);
    float sine = sin(angle);
    local = mat2(cosine, -sine, sine, cosine) * local;
    vec2 scaled = local / size;
    float body = exp(-scaled.x * scaled.x * 3.6 - scaled.y * scaled.y * 20.0);
    float head = exp(-pow((scaled.x - 0.24) * 3.0, 2.0) - pow(scaled.y * 6.0, 2.0)) * 0.38;
    float tail = exp(-pow((scaled.x + 0.56) * 2.1, 2.0) - pow(scaled.y * 7.0, 2.0)) * 0.82;
    return clamp(body + head + tail, 0.0, 1.0);
  }

  float spriteBounds(vec2 uv) {
    return step(0.0, uv.x) * step(uv.x, 1.0) * step(0.0, uv.y) * step(uv.y, 1.0);
  }

  vec4 sampleKoi(sampler2D sprite, vec2 uv, vec2 center, float angle, vec2 size, float phase, float swimClock, float effort, float turnBend, float headDirection) {
    vec2 spriteUv = fishSpriteUv(uv, center, angle, size, phase, swimClock, effort, turnBend, headDirection);
    vec2 safeUv = clamp(spriteUv, vec2(0.0), vec2(1.0));
    vec4 fish = texture2D(sprite, safeUv) * 0.84;
    fish += texture2D(sprite, clamp(safeUv + vec2(0.0018, 0.0), vec2(0.0), vec2(1.0))) * 0.15;
    fish += texture2D(sprite, clamp(safeUv - vec2(0.0018, 0.0), vec2(0.0), vec2(1.0))) * 0.15;
    fish.a *= spriteBounds(spriteUv);
    return fish;
  }

  vec4 fishField(vec2 uv) {
    float seed = uFishSeed;
    float aspect = uResolution.x / max(uResolution.y, 1.0);
    float responsiveScale = clamp(aspect / 0.75, 0.62, 1.0);
    float x1 = -0.12 + mod(uTime * 0.052 + seed * 0.17, 1.24);
    vec2 p1 = vec2(x1, 0.24 + sin(uTime * 0.56 + seed) * 0.045);
    p1 += uFishOffset1;
    float panic1 = uFishEffort.x;
    float baseAngle1 = cos(uTime * 0.52 + seed) * 0.1;
    vec2 turn1 = turnResponse(baseAngle1, -safeAngle(uFishEscape1, 0.0), panic1);
    float angle1 = turn1.x;
    vec2 size1 = vec2(0.18, 0.072) * responsiveScale;
    vec4 fish1 = sampleKoi(uKohakuKoi, uv, p1, angle1, size1, seed, uFishSwimClock.x, panic1, turn1.y, 1.0);
    vec2 dir1 = safeNormalize(vec2(cos(angle1), sin(angle1)));
    float aura1 = fishAura(uv, p1, angle1, size1);
    float glow1 = fishWake(uv, p1, aspect, 17.0, 0.018 + panic1 * 0.04);
    float trail1 = fishWake(uv, p1 - dir1 * 0.03, aspect, 14.0, 0.01 + panic1 * 0.025);
    fish1.rgb = mix(fish1.rgb, uLight * vec3(0.78, 0.95, 0.94), 0.04 + panic1 * 0.05);
    fish1.a = clamp(fish1.a, 0.0, 1.0);

    float x2 = 1.12 - mod(uTime * 0.04 + seed * 0.29, 1.24);
    vec2 p2 = vec2(x2, 0.69 + sin(uTime * 0.48 + seed * 1.7) * 0.055);
    p2 += uFishOffset2;
    float panic2 = uFishEffort.y;
    float baseAngle2 = -cos(uTime * 0.43 + seed) * 0.12;
    vec2 turn2 = turnResponse(baseAngle2, safeAngle(uFishEscape2, 0.0), panic2);
    float angle2 = turn2.x;
    vec2 size2 = vec2(0.2, 0.062) * responsiveScale;
    vec4 fish2 = sampleKoi(uShowaKoi, uv, p2, angle2, size2, seed + 2.1, uFishSwimClock.y, panic2, turn2.y, -1.0);
    vec2 dir2 = safeNormalize(vec2(cos(angle2), sin(angle2)));
    float aura2 = fishAura(uv, p2, angle2, size2);
    float glow2 = fishWake(uv, p2, aspect, 17.0, 0.018 + panic2 * 0.04);
    float trail2 = fishWake(uv, p2 - dir2 * 0.03, aspect, 14.0, 0.01 + panic2 * 0.025);
    fish2.rgb = mix(fish2.rgb, uLight * vec3(0.78, 0.95, 0.94), 0.04 + panic2 * 0.05);
    fish2.a = clamp(fish2.a, 0.0, 1.0);

    float x3 = -0.12 + mod(uTime * 0.034 + seed * 0.41, 1.24);
    vec2 p3 = vec2(x3, 0.5 + sin(uTime * 0.42 + seed * 0.73) * 0.07);
    p3 += uFishOffset3;
    float panic3 = uFishEffort.z;
    float baseAngle3 = cos(uTime * 0.37 + seed) * 0.15;
    vec2 turn3 = turnResponse(baseAngle3, -safeAngle(uFishEscape3, 0.0), panic3);
    float angle3 = turn3.x;
    vec2 size3 = vec2(0.23, 0.092) * responsiveScale;
    vec4 fish3 = sampleKoi(uYamabukiKoi, uv, p3, angle3, size3, seed + 4.3, uFishSwimClock.z, panic3, turn3.y, 1.0);
    vec2 dir3 = safeNormalize(vec2(cos(angle3), sin(angle3)));
    float aura3 = fishAura(uv, p3, angle3, size3);
    float glow3 = fishWake(uv, p3, aspect, 16.0, 0.02 + panic3 * 0.04);
    float trail3 = fishWake(uv, p3 - dir3 * 0.036, aspect, 14.0, 0.012 + panic3 * 0.026);
    fish3.rgb = mix(fish3.rgb, uLight * vec3(0.78, 0.95, 0.94), 0.04 + panic3 * 0.05);
    fish3.a = clamp(fish3.a, 0.0, 1.0);

    float x4 = 1.12 - mod(uTime * 0.046 + seed * 0.53, 1.24);
    vec2 p4 = vec2(x4, 0.84 + sin(uTime * 0.66 + seed * 1.31) * 0.035);
    p4 += uFishOffset4;
    float panic4 = uFishEffort.w;
    float baseAngle4 = -cos(uTime * 0.61 + seed) * 0.09;
    vec2 turn4 = turnResponse(baseAngle4, safeAngle(uFishEscape4, 0.0), panic4);
    float angle4 = turn4.x;
    vec2 size4 = vec2(0.16, 0.052) * responsiveScale;
    vec4 fish4 = sampleKoi(uShiroKoi, uv, p4, angle4, size4, seed + 6.7, uFishSwimClock.w, panic4, turn4.y, -1.0);
    vec2 dir4 = safeNormalize(vec2(cos(angle4), sin(angle4)));
    float aura4 = fishAura(uv, p4, angle4, size4);
    float glow4 = fishWake(uv, p4, aspect, 17.0, 0.016 + panic4 * 0.035);
    float trail4 = fishWake(uv, p4 - dir4 * 0.028, aspect, 14.0, 0.009 + panic4 * 0.022);
    fish4.rgb = mix(fish4.rgb, uLight * vec3(0.78, 0.95, 0.94), 0.04 + panic4 * 0.05);
    fish4.a = clamp(fish4.a, 0.0, 1.0);

    float alpha = fish1.a + fish2.a + fish3.a + fish4.a;
    float aura = clamp(aura1 + aura2 + aura3 + aura4, 0.0, 1.0);
    vec3 fishBodyTint = mix(uGlow * 0.72, uLight * 0.84, 0.42);
    vec3 rgb = (fish1.rgb * fish1.a + fish2.rgb * fish2.a + fish3.rgb * fish3.a + fish4.rgb * fish4.a) / max(alpha, 0.001);
    rgb = mix(rgb, fishBodyTint, clamp(aura * 0.08, 0.0, 0.12));
    float glow = glow1 + glow2 + glow3 + glow4;
    float trail = trail1 + trail2 + trail3 + trail4;
    rgb += uLight * glow * 0.08;
    rgb += uGlow * trail * 0.05;
    return vec4(rgb, clamp(alpha, 0.0, 1.0));
  }

  void main() {
    float pixel = 1.8 / max(min(uResolution.x, uResolution.y), 1.0);
    float height = surfaceHeight(vUv);
    float heightX = surfaceHeight(vUv + vec2(pixel, 0.0));
    float heightY = surfaceHeight(vUv + vec2(0.0, pixel));
    vec2 gradient = vec2(heightX - height, heightY - height) / pixel;

    float aspect = uResolution.x / max(uResolution.y, 1.0);
    vec2 p = (vUv - 0.5) * vec2(aspect, 1.0);
    float edgeFade = smoothstep(0.0, 0.07, vUv.x) * smoothstep(0.0, 0.07, 1.0 - vUv.x);
    edgeFade *= smoothstep(0.0, 0.07, vUv.y) * smoothstep(0.0, 0.07, 1.0 - vUv.y);
    vec2 refractedUv = vUv + gradient * vec2(0.0085, 0.0065) * edgeFade;
    vec2 textureUv = clamp(coverUv(refractedUv), vec2(0.002), vec2(0.998));
    vec3 stones = texture2D(uStones, textureUv).rgb;

    float caustic = causticField(p + gradient * 0.12);
    float slope = clamp(length(gradient) * 0.72, 0.0, 1.0);

    vec3 normal = normalize(vec3(-gradient.x * 1.5, -gradient.y * 1.5, 1.0));
    vec3 lightDirection = normalize(vec3(-0.42, 0.28, 0.86));
    float specular = pow(max(dot(normal, lightDirection), 0.0), 180.0);
    float shimmer = 0.5 + 0.5 * sin(uTime * 0.42 + p.x * 2.4 - p.y * 1.7);

    vec4 fish = fishField(vUv);
    float fishMask = smoothstep(0.08, 0.5, fish.a);
    vec3 underwaterFish = fish.rgb * vec3(0.76, 0.9, 0.92);
    underwaterFish = mix(underwaterFish, underwaterFish * vec3(0.88, 0.96, 0.98), slope * 0.12);
    vec3 causticLight = mix(vec3(0.78, 0.92, 0.9), vec3(1.0), 0.42);
    vec3 wetStones = pow(max(stones, vec3(0.001)), vec3(0.88)) * 1.06;
    wetStones = mix(wetStones, wetStones * vec3(0.9, 0.98, 1.02), 0.18);
    wetStones = mix(wetStones, uDeep * max(dot(stones, vec3(0.22, 0.7, 0.08)), 0.0), 0.025);

    vec3 color = mix(wetStones, underwaterFish, fishMask);
    color += causticLight * caustic * (0.32 + slope * 0.1);
    color += vec3(0.92, 1.0, 0.99) * specular * (0.16 + shimmer * 0.06);

    float vignette = smoothstep(0.92, 0.24, length((vUv - 0.5) * vec2(0.82, 1.0)));
    color *= mix(0.9, 1.06, vignette);
    gl_FragColor = vec4(color, 1.0);
  }
`

function applyPalette() {
  if (!waterPlane) return
  const uniforms = waterPlane.material.uniforms
  uniforms.uDeep.value.set(palette.value.deep)
  uniforms.uMid.value.set(palette.value.mid)
  uniforms.uGlow.value.set(palette.value.glow)
  uniforms.uLight.value.set(palette.value.light)
}

function smoothUnit(value: number) {
  const clamped = THREE.MathUtils.clamp(value, 0, 1)
  return clamped * clamped * (3 - 2 * clamped)
}

function resetFishMotion() {
  fishMotionStates.forEach((state, index) => {
    state.offset.set(0, 0)
    state.velocity.set(0, 0)
    state.escapeDirection.set(0, index % 2 === 0 ? 1 : -1)
    state.effort = 0
    state.startleTime = 0
    state.startleStrength = 0
    state.threatLatched = false
    state.swimClock = 0
  })
}

function updateFishCenters(time: number) {
  fishCenters[0].set(
    -0.12 + THREE.MathUtils.euclideanModulo(time * 0.052 + fishSeed * 0.17, 1.24),
    0.24 + Math.sin(time * 0.56 + fishSeed) * 0.045,
  )
  fishCenters[1].set(
    1.12 - THREE.MathUtils.euclideanModulo(time * 0.04 + fishSeed * 0.29, 1.24),
    0.69 + Math.sin(time * 0.48 + fishSeed * 1.7) * 0.055,
  )
  fishCenters[2].set(
    -0.12 + THREE.MathUtils.euclideanModulo(time * 0.034 + fishSeed * 0.41, 1.24),
    0.5 + Math.sin(time * 0.42 + fishSeed * 0.73) * 0.07,
  )
  fishCenters[3].set(
    1.12 - THREE.MathUtils.euclideanModulo(time * 0.046 + fishSeed * 0.53, 1.24),
    0.84 + Math.sin(time * 0.66 + fishSeed * 1.31) * 0.035,
  )
}

function updateFishMotion(time: number, deltaSeconds: number) {
  if (!waterPlane || !rootHeight) return

  updateFishCenters(time)
  const aspect = rootWidth / rootHeight

  fishMotionStates.forEach((state, index) => {
    const config = fishMotionConfigs[index]
    fishMotionCenter.copy(fishCenters[index]).add(state.offset)
    fishMotionDelta.copy(fishMotionCenter).sub(pointer)
    fishMotionDelta.x *= aspect

    const distance = fishMotionDelta.length()
    const rawThreat = pointerActive
      ? 1 - THREE.MathUtils.smoothstep(distance, config.radius * 0.04, config.radius * 1.12)
      : 0
    const targetThreat = rawThreat * rawThreat * (3 - 2 * rawThreat)

    if (distance > 0.0001 && targetThreat > 0.001) {
      fishMotionDelta.multiplyScalar(1 / distance)
      fishMotionDelta.x /= aspect
      state.escapeDirection.lerp(fishMotionDelta, 1 - Math.exp(-14 * deltaSeconds))
    }

    if (targetThreat > 0.16 && !state.threatLatched) {
      state.threatLatched = true
      state.startleTime = 0.0001
      state.startleStrength = THREE.MathUtils.clamp(0.58 + targetThreat * 0.42, 0, 1)
    } else if (targetThreat < 0.06) {
      state.threatLatched = false
    }

    let targetEffort = 0
    let propulsion = 0
    let tailRateBoost = 0

    if (state.startleTime > 0) {
      state.startleTime += deltaSeconds

      if (state.startleTime < 0.11) {
        const coil = smoothUnit(state.startleTime / 0.11)
        targetEffort = coil * state.startleStrength
        tailRateBoost = coil * 0.18
      } else if (state.startleTime < 0.34) {
        const burst = smoothUnit((state.startleTime - 0.11) / 0.23)
        targetEffort = THREE.MathUtils.lerp(1, 0.65, burst) * state.startleStrength
        propulsion = Math.sin(Math.PI * burst) ** 2
        tailRateBoost = THREE.MathUtils.lerp(1.3, 0.9, burst)
      } else if (state.startleTime < 0.88) {
        const coast = smoothUnit((state.startleTime - 0.34) / 0.54)
        targetEffort = 0.65 * (1 - coast) * state.startleStrength
        tailRateBoost = 0.28 * (1 - coast)
      } else {
        state.startleTime = 0
        state.startleStrength = 0
      }
    }

    const responseRate = targetEffort > state.effort ? 18 : 5
    state.effort += (targetEffort - state.effort) * (1 - Math.exp(-responseRate * deltaSeconds))

    const stylePhase = fishSeed + fishPhaseOffsets[index]
    const motionVariation = 0.5 + 0.5 * Math.sin(stylePhase * 1.731 + 0.7)
    const cruiseRate = THREE.MathUtils.lerp(3.7, 6.4, motionVariation)
    state.swimClock = THREE.MathUtils.euclideanModulo(
      state.swimClock + cruiseRate * (1 + tailRateBoost) * deltaSeconds,
      Math.PI * 2,
    )

    const acceleration = config.strength * 34 * state.startleStrength * propulsion
    state.velocity.addScaledVector(state.escapeDirection, acceleration * deltaSeconds)
    state.velocity.addScaledVector(
      state.offset,
      -6.8 * (1 - targetThreat * 0.85) * deltaSeconds,
    )
    const damping = state.startleTime > 0 ? (propulsion > 0 ? 2.1 : 2.8) : 4.2
    state.velocity.multiplyScalar(Math.exp(-damping * deltaSeconds))

    const screenVelocityX = state.velocity.x * aspect
    const screenSpeed = Math.hypot(screenVelocityX, state.velocity.y)
    const maxSpeed = config.strength * 4.5
    if (screenSpeed > maxSpeed) {
      state.velocity.multiplyScalar(maxSpeed / screenSpeed)
    }

    state.offset.addScaledVector(state.velocity, deltaSeconds)
    const screenOffsetX = state.offset.x * aspect
    const screenOffset = Math.hypot(screenOffsetX, state.offset.y)
    const maxOffset = config.strength * 1.35
    if (screenOffset > maxOffset) {
      state.offset.multiplyScalar(maxOffset / screenOffset)
      if (state.velocity.dot(state.offset) > 0) state.velocity.multiplyScalar(0.45)
    }
  })

  const uniforms = waterPlane.material.uniforms
  uniforms.uFishEffort.value.set(
    fishMotionStates[0].effort,
    fishMotionStates[1].effort,
    fishMotionStates[2].effort,
    fishMotionStates[3].effort,
  )
  uniforms.uFishSwimClock.value.set(
    fishMotionStates[0].swimClock,
    fishMotionStates[1].swimClock,
    fishMotionStates[2].swimClock,
    fishMotionStates[3].swimClock,
  )
  uniforms.uFishOffset1.value.copy(fishMotionStates[0].offset)
  uniforms.uFishOffset2.value.copy(fishMotionStates[1].offset)
  uniforms.uFishOffset3.value.copy(fishMotionStates[2].offset)
  uniforms.uFishOffset4.value.copy(fishMotionStates[3].offset)
  uniforms.uFishEscape1.value.copy(fishMotionStates[0].escapeDirection)
  uniforms.uFishEscape2.value.copy(fishMotionStates[1].escapeDirection)
  uniforms.uFishEscape3.value.copy(fishMotionStates[2].escapeDirection)
  uniforms.uFishEscape4.value.copy(fishMotionStates[3].escapeDirection)
}

async function initScene() {
  const canvas = canvasRef.value
  const root = rootRef.value
  if (!canvas || !root) return

  renderer = new THREE.WebGLRenderer({
    canvas,
    alpha: false,
    antialias: true,
    powerPreference: 'high-performance',
  })
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
  renderer.setClearColor(0x000000, 0)
  renderer.outputColorSpace = THREE.SRGBColorSpace

  scene = new THREE.Scene()
  camera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1)

  try {
    const loader = new THREE.TextureLoader()
    ;[
      stonesTexture,
      kohakuKoiTexture,
      showaKoiTexture,
      yamabukiKoiTexture,
      shiroKoiTexture,
    ] = await Promise.all([
      loader.loadAsync(riverbedStonesSrc),
      loader.loadAsync(kohakuKoiSrc),
      loader.loadAsync(showaKoiSrc),
      loader.loadAsync(yamabukiKoiSrc),
      loader.loadAsync(shiroKoiSrc),
    ])
  } catch {
    return
  }
  if (
    destroyed ||
    !renderer ||
    !scene ||
    !camera ||
    !stonesTexture ||
    !kohakuKoiTexture ||
    !showaKoiTexture ||
    !yamabukiKoiTexture ||
    !shiroKoiTexture
  ) {
    stonesTexture?.dispose()
    kohakuKoiTexture?.dispose()
    showaKoiTexture?.dispose()
    yamabukiKoiTexture?.dispose()
    shiroKoiTexture?.dispose()
    return
  }

  for (const texture of [
    stonesTexture,
    kohakuKoiTexture,
    showaKoiTexture,
    yamabukiKoiTexture,
    shiroKoiTexture,
  ]) {
    texture.colorSpace = THREE.SRGBColorSpace
    texture.minFilter = THREE.LinearFilter
    texture.magFilter = THREE.LinearFilter
    texture.anisotropy = Math.min(renderer.capabilities.getMaxAnisotropy(), 8)
  }

  fishSeed = Math.random() * 100
  lastFrameTime = 0
  resetFishMotion()

  const material = new THREE.ShaderMaterial({
    uniforms: {
      uStones: { value: stonesTexture },
      uKohakuKoi: { value: kohakuKoiTexture },
      uShowaKoi: { value: showaKoiTexture },
      uYamabukiKoi: { value: yamabukiKoiTexture },
      uShiroKoi: { value: shiroKoiTexture },
      uResolution: { value: new THREE.Vector2(1, 1) },
      uTextureResolution: { value: new THREE.Vector2(1920, 1920) },
      uPointer: { value: pointer.clone() },
      uPulseOrigin: { value: pulseOrigin.clone() },
      uPointerEnergy: { value: 0 },
      uPulse: { value: 0 },
      uTime: { value: 0 },
      uFishSeed: { value: fishSeed },
      uFishEffort: { value: new THREE.Vector4() },
      uFishSwimClock: { value: new THREE.Vector4() },
      uFishOffset1: { value: new THREE.Vector2() },
      uFishOffset2: { value: new THREE.Vector2() },
      uFishOffset3: { value: new THREE.Vector2() },
      uFishOffset4: { value: new THREE.Vector2() },
      uFishEscape1: { value: fishMotionStates[0].escapeDirection.clone() },
      uFishEscape2: { value: fishMotionStates[1].escapeDirection.clone() },
      uFishEscape3: { value: fishMotionStates[2].escapeDirection.clone() },
      uFishEscape4: { value: fishMotionStates[3].escapeDirection.clone() },
      uDeep: { value: new THREE.Color(palette.value.deep) },
      uMid: { value: new THREE.Color(palette.value.mid) },
      uGlow: { value: new THREE.Color(palette.value.glow) },
      uLight: { value: new THREE.Color(palette.value.light) },
    },
    vertexShader,
    fragmentShader,
    depthTest: false,
    depthWrite: false,
  })

  waterPlane = new THREE.Mesh(new THREE.PlaneGeometry(2, 2), material)
  scene.add(waterPlane)

  resizeRenderer()
  resizeObserver = new ResizeObserver(resizeRenderer)
  resizeObserver.observe(root)
  window.addEventListener('pointermove', handlePointerMove, { passive: true })
  window.addEventListener('pointerdown', handlePointerDown, { passive: true })
  animate()
}

function resizeRenderer() {
  const root = rootRef.value
  if (!renderer || !waterPlane || !root) return
  const width = root.clientWidth
  const height = root.clientHeight
  if (!width || !height || (width === rootWidth && height === rootHeight)) return

  rootWidth = width
  rootHeight = height
  renderer.setSize(width, height, false)
  waterPlane.material.uniforms.uResolution.value.set(width, height)
}

function updatePointer(event: PointerEvent) {
  const root = rootRef.value
  if (!root) return
  const rect = root.getBoundingClientRect()
  if (!rect.width || !rect.height) return

  const nextX = THREE.MathUtils.clamp((event.clientX - rect.left) / rect.width, 0, 1)
  const nextY = THREE.MathUtils.clamp(1 - (event.clientY - rect.top) / rect.height, 0, 1)
  const distance = pointerTarget.distanceTo(new THREE.Vector2(nextX, nextY))
  pointerActive = true
  pointerTarget.set(nextX, nextY)
  pointerEnergy = Math.min(0.55, pointerEnergy + distance * 2.4)
}

function handlePointerMove(event: PointerEvent) {
  updatePointer(event)
}

function handlePointerDown(event: PointerEvent) {
  updatePointer(event)
  pulseOrigin.copy(pointerTarget)
  interactionPulse = 1
}

function animate() {
  if (!renderer || !scene || !camera || !waterPlane) return

  const frameTime = performance.now()
  const time = frameTime * 0.001
  const deltaSeconds = lastFrameTime ? Math.min((frameTime - lastFrameTime) * 0.001, 0.033) : 1 / 60
  lastFrameTime = frameTime
  pointer.lerp(pointerTarget, 0.09)
  interactionPulse *= 0.955
  pointerEnergy *= 0.9
  updateFishMotion(time, deltaSeconds)

  const uniforms = waterPlane.material.uniforms
  uniforms.uTime.value = time
  uniforms.uPointer.value.copy(pointer)
  uniforms.uPulseOrigin.value.copy(pulseOrigin)
  uniforms.uPointerEnergy.value = pointerEnergy
  uniforms.uPulse.value = interactionPulse

  renderer.render(scene, camera)
  frameId = window.requestAnimationFrame(animate)
}

watch(palette, applyPalette)

onMounted(() => {
  destroyed = false
  void initScene()
})

onBeforeUnmount(() => {
  destroyed = true
  if (frameId !== undefined) window.cancelAnimationFrame(frameId)
  resizeObserver?.disconnect()
  window.removeEventListener('pointermove', handlePointerMove)
  window.removeEventListener('pointerdown', handlePointerDown)
  waterPlane?.geometry.dispose()
  waterPlane?.material.dispose()
  stonesTexture?.dispose()
  kohakuKoiTexture?.dispose()
  showaKoiTexture?.dispose()
  yamabukiKoiTexture?.dispose()
  shiroKoiTexture?.dispose()
  renderer?.dispose()
})
</script>

<template>
  <div ref="rootRef" class="water-auth-scene" :style="sceneStyle" aria-hidden="true">
    <canvas ref="canvasRef" class="water-auth-scene-canvas"></canvas>
    <div class="water-auth-scene-overlay"></div>
  </div>
</template>

<style scoped>
.water-auth-scene {
  position: absolute;
  inset: 0;
  overflow: hidden;
  pointer-events: none;
  background-color: var(--water-auth-deep);
  background-image: var(--water-auth-stones);
  background-position: center;
  background-size: cover;
}

.water-auth-scene-canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  display: block;
}

.water-auth-scene-overlay {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(circle at 50% 44%, transparent 0, transparent 38%, rgba(2, 22, 29, 0.08) 100%),
    linear-gradient(180deg, rgba(255, 255, 255, 0.035), transparent 22%, rgba(2, 20, 27, 0.08));
}
</style>
