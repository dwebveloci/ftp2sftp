# Discovery técnico — FTP2SFTP Gateway

Estado: fase de descubrimiento. No contiene diseño de API, elección de librerías
ni patrones de implementación. Objetivo: entender el problema por completo
antes de diseñar la solución.

Fuentes inspeccionadas: `CLAUDE.md`, `FTP2SFTP-REQUIREMENTS.md`, estado actual
del repositorio (`cmd/`, `internal/`, `docs/`, `ADR/` — todos son stubs o
plantillas vacías, no hay implementación previa que condicione este análisis).

---

## 1. Dominio del problema

No es un cliente FTP, ni un proxy TCP transparente, ni un servidor SFTP. Es un
**gateway de aplicación con traducción de protocolo con estado**, que:

- Habla FTP en el lado "sur" (cliente: AX 2012, protocolo legado, texto plano,
  dos canales — control y datos).
- Habla SFTP-sobre-SSH en el lado "norte" (protocolo binario, orientado a
  paquetes, un solo canal cifrado con subcanales lógicos).
- Traduce **operaciones**, no bytes. STOR no es "reenviar un socket a otro":
  implica autenticación, autorización, resolución de ruta virtual→real,
  streaming con backpressure, nombre temporal, rename atómico, limpieza ante
  fallo y auditoría.

El dominio combina tres cuerpos de conocimiento independientes que no deben
tratarse como intercambiables (explícito en `CLAUDE.md`):

1. **Protocolo FTP** como máquina de estados por sesión (auth → cwd →
   modo de datos → comando de transferencia → cierre de canal de datos).
2. **Protocolo SFTP/SSH** como sesión autenticada con semántica de solicitud/
   respuesta asíncrona sobre un canal SSH.
3. **Integridad de transferencia de archivos** como problema transversal
   (atomicidad, archivos parciales, backpressure, límites de recursos)
   independiente de qué protocolo se use en cada extremo.

La complejidad real no está en ningún protocolo aislado, sino en la
**correlación de ciclos de vida distintos**: una sesión FTP puede vivir mucho
más o mucho menos que una conexión SSH, y una transferencia STOR debe
mantenerse consistente aunque cualquiera de los dos lados falle en cualquier
punto.

## 2. Límites arquitectónicos

Límites de confianza y de proceso identificados:

- **Límite de confianza 1 — cliente FTP (AX) ↔ gateway.** Canal no cifrado.
  Cualquier control de seguridad aquí depende de la red (privada/túnel), no
  del protocolo. El gateway debe tratar toda entrada de AX como no confiable
  (rutas, nombres de archivo, secuencias de comandos, tamaños declarados).
- **Límite de confianza 2 — gateway ↔ servidor SFTP remoto.** Canal cifrado y
  autenticado, pero el gateway sigue sin confiar en las respuestas remotas
  para garantías de seguridad propias (p. ej. no asumir que el chroot remoto
  es suficiente; debe aplicar su propia restricción de raíz virtual).
- **Límite de proceso — gateway como monolito modular único.** Un solo
  binario, un solo proceso, memoria compartida entre módulos in-process. No
  hay límite de red interno; los "límites" internos son de paquete/módulo Go,
  no de despliegue.
- **Límite de datos — sin persistencia propia significativa.** El gateway no
  es dueño de los archivos a largo plazo; es un tránsito. El único estado
  persistente potencial son temporales in-flight y `known_hosts`/configuración
  no secreta.
- **Límite de despliegue — red interna vs. túnel Cloudflare vs. Internet.**
  No está definido qué segmento de red toca cada interfaz (ver §10, decisión
  pendiente #4/#5). Este es un límite arquitectónico real que hoy es
  ambiguo.

## 3. Módulos principales

Usando la estructura ya sugerida en los requerimientos (`internal/*`) como
inventario de responsabilidades (no como diseño final):

- `ftpserver` — listener y ciclo de vida de conexión FTP.
- `ftpcommand` — parsing/dispatch de comandos FTP.
- `session` — estado por sesión FTP.
- `auth` — autenticación de credenciales FTP.
- `authorization` — política de comandos/rutas/permisos permitidos.
- `filesystem` — normalización y mapeo de rutas virtuales → reales.
- `sshclient` — ciclo de vida de conexión SSH y verificación de host key.
- `sftpclient` — operaciones SFTP sobre el canal SSH.
- `transfer` — orquestación de subida/descarga, streaming, commit atómico.
- `audit` — eventos estructurados de auditoría.
- `observability` — logs, métricas, correlación.
- `health` — health/readiness.
- `errors` — taxonomía y mapeo de errores.
- `config` — carga y validación de configuración al arranque.

## 4. Responsabilidades de cada módulo

| Módulo | Posee | No posee |
|---|---|---|
| `ftpserver` | socket de control, framing de comandos, códigos de respuesta FTP, canal de datos (activo/pasivo) | reglas de autorización, mapeo de rutas, lógica SFTP |
| `session` | estado mutable por conexión (usuario, cwd virtual, modo, transferencia en curso) | validación de credenciales, decisión de autorización |
| `auth` | verificación de credenciales, protección contra fuerza bruta | gestión de sesión, autorización por ruta |
| `authorization` | qué comandos/rutas/operaciones puede hacer un usuario ya autenticado | traducción de protocolo, ejecución de I/O |
| `filesystem` | normalización de rutas, resolución de raíz virtual, detección de traversal | ejecución de operaciones remotas |
| `sshclient` | handshake SSH, verificación de host key, algoritmos permitidos | semántica de archivos/SFTP |
| `sftpclient` | operaciones de archivo remoto (open/write/rename/stat/list), traducción de errores SFTP→dominio | política de negocio (qué se permite), commit atómico como estrategia |
| `transfer` | orquestación end-to-end de una transferencia: streaming, backpressure, nombre temporal, rename final, limpieza | parsing de comandos FTP, autenticación |
| `audit` | emisión de eventos estructurados con campos fijos (RF-015) | decisión de qué es "sensible" (eso es política de `observability`/`errors`) |
| `errors` | taxonomía interna y mapeo a códigos FTP (§11) | logging directo (delega a `observability`) |

**Riesgo de diseño detectado aquí:** el requerimiento pone la
"traducción de errores SFTP a errores de dominio" en `sftpclient` (§6.2) y el
"mapeo conceptual a códigos FTP" en la capa de errores/`ftpserver`. Si no se
define con precisión dónde termina un mapeo y empieza el otro, es fácil
terminar con dos lugares distintos decidiendo el mismo código FTP para el
mismo fallo remoto (duplicación de lógica de mapeo, inconsistencias futuras).
Esto no está resuelto por los requerimientos; es una decisión de diseño
pendiente, no solo de nomenclatura.

## 5. Flujos principales del sistema

1. **Arranque:** cargar configuración → validar (rango pasivo, `known_hosts`,
   usuarios duplicados, raíces inseguras, límites) → fallar rápido si inválida
   → iniciar listener FTP → exponer health/readiness.
2. **Conexión y autenticación FTP:** `USER`/`PASS` → validar contra almacén →
   éxito/fracaso auditado sin contraseña en log → crear sesión con raíz
   virtual asociada al usuario.
3. **Navegación:** `PWD`/`CWD`/`CDUP` operan solo sobre el estado virtual de
   la sesión; toda resolución de ruta pasa por `filesystem` antes de tocar
   `sftpclient`.
4. **Subida (`STOR`) — el flujo crítico del sistema:**
   auth → autorización → normalización de ruta → apertura de canal de datos
   FTP (pasivo/activo) → apertura de destino temporal remoto
   (`archivo.xml.part-<session-id>`) → streaming con backpressure acotada →
   detección de EOF/errores → cierre de ambos extremos → verificación de
   tamaño → `rename` remoto atómico → auditoría → limpieza de temporal si algo
   falla en cualquier paso.
5. **Descarga (`RETR`, si se habilita):** streaming inverso SFTP→FTP sin
   materializar el archivo completo en memoria.
6. **Listado (`LIST`/`NLST`):** traducción de `readdir` remoto a formato de
   texto compatible con el parser real de AX (no genérico).
7. **Rename (`RNFR`/`RNTO`):** operación de dos comandos ligada a la misma
   sesión, con validación de ambas rutas y manejo de conflicto.
8. **Cierre de sesión:** liberar socket de control, socket de datos, archivos
   abiertos, canal SFTP, sesión SSH (si no se reutiliza), goroutines,
   temporales huérfanos cuando sea seguro hacerlo.
9. **Apagado del proceso (`SIGTERM`):** dejar de aceptar → drenar
   transferencias en curso dentro de un plazo → cancelar el resto → cerrar
   canales/sesiones → marcar/limpiar temporales → flush de logs/métricas →
   salir.

## 6. Riesgos técnicos

- **Atomicidad del rename remoto no garantizada por el protocolo.** SFTP v3
  (la versión más comúnmente soportada) no garantiza que `rename` sobrescriba
  un destino existente; muchos servidores solo lo permiten vía la extensión
  `posix-rename@openssh.com`. Si el servidor remoto no la soporta, el `rename`
  final puede fallar cuando el archivo destino ya existe, lo que rompe la
  estrategia de commit atómico descrita en RF-007 tal como está planteada. No
  está confirmado qué software corre el servidor SFTP remoto ni si soporta
  esa extensión — esto es una dependencia crítica no verificada.
- **Correlación de ciclos de vida FTP↔SSH.** Si AX abre y cierra una conexión
  FTP de control por archivo (patrón común en integraciones legadas por lotes),
  el modelo "una sesión SSH por sesión FTP" (preferido para el MVP, §10.2)
  puede generar un handshake SSH completo por archivo. Esto no es un defecto
  de diseño per se, pero es el primer supuesto de rendimiento que debe
  validarse empíricamente antes de asumir que la simplicidad no tiene costo
  operativo.
- **Backpressure entre dos protocolos con framing distinto.** FTP en modo
  streaming no tiene flow control propio explícito a nivel de aplicación (se
  apoya en TCP); SFTP sí tiene un protocolo de solicitud/respuesta con
  ventanas de escritura. Acoplar ambos flujos de forma correcta (que un
  escritor SFTP lento realmente frene la lectura del socket FTP) es no
  trivial y es una fuente típica de fugas de memoria si se hace mal.
- **Tamaño y framing de comandos FTP.** No hay mención de límite de longitud
  de línea de comando ni de manejo de comandos malformados/binarios inyectados
  en el canal de control. Un parser laxo es superficie de errores y de DoS.
- **Formato de `LIST` no verificado contra AX.** El documento ya lo marca como
  obligatorio de validar (§15.4), pero es un riesgo técnico transversal: si
  AX 2012 espera un formato de listado específico (estilo MS-DOS o Unix con
  campos exactos), construir el parser antes de capturar tráfico real es
  trabajo con alta probabilidad de rehacerse.
- **Manejo de `TYPE A` (ASCII) vs `TYPE I` (binario).** No se especifica si el
  gateway debe soportar transferencia en modo ASCII con traducción de fin de
  línea. Aplicar traducción a archivos que no la necesitan (XML, binarios)
  corrompería datos; no soportar `TYPE A` en absoluto podría romper
  compatibilidad si AX lo solicita por defecto.

## 7. Riesgos de seguridad

- **FTP es texto plano por diseño.** Usuario, contraseña y contenido viajan
  sin cifrar entre AX y el gateway. La única mitigación posible es de red
  (red privada o túnel), no de protocolo — el documento ya lo reconoce
  (§9.5), pero esto significa que la superficie de seguridad del sistema
  completo depende de una decisión de red que hoy no está tomada (decisión
  pendiente #4/#5). Sin esa decisión, cualquier afirmación de "el sistema es
  seguro" es incompleta.
- **Traversal de rutas en dos capas distintas.** La normalización debe
  ocurrir tanto en el lado virtual (FTP) como considerar el comportamiento de
  symlinks en el lado remoto (SFTP), que el gateway no controla directamente.
  Un enlace simbólico creado en el servidor remoto que apunte fuera de la raíz
  configurada puede eludir controles puramente virtuales si `sftpclient` no
  aplica `Lstat`/resolución defensiva antes de operar.
- **Confinamiento remoto fuera del control del gateway.** El documento asume
  "usuario remoto restringido exclusivamente a SFTP" y "acceso limitado a una
  ruta específica" (§2.3) como control del lado del servidor SFTP. El gateway
  no puede verificar ni hacer cumplir esa restricción; si la cuenta remota
  está mal configurada (chroot ausente o incorrecto), la única barrera real
  es la lógica de rutas del propio gateway. Esto es una dependencia de
  seguridad externa no verificable desde el código.
- **Superficie de fuerza bruta por sesión de control persistente.** FTP
  permite múltiples intentos `USER`/`PASS` en la misma conexión TCP; el
  límite por IP/usuario debe aplicar tanto a nivel de conexión como de
  intentos dentro de una conexión, o el rate limiting es trivialmente
  evadible reutilizando la misma conexión.
- **Visibilidad de archivos temporales vía `LIST`.** Si `LIST`/`NLST` reflejan
  directamente el directorio remoto, el archivo `*.part-<session-id>` sería
  visible a cualquier cliente que liste ese directorio durante la
  transferencia, incluyendo teóricamente otra sesión FTP con acceso a la
  misma ruta virtual. Esto no es solo un problema estético: puede filtrar
  IDs de sesión y el hecho de que una transferencia está en curso, o
  confundir a un consumidor automatizado que puede intentar leer temporales.
  No hay requerimiento que exija filtrar `*.part*` del listado.
- **Secretos y llaves cruzando el límite de confianza equivocado.** Ya
  cubierto en requerimientos (§9.2), pero vale remarcar el riesgo compuesto:
  la llave privada SFTP vive en el mismo proceso que atiende conexiones FTP
  no autenticadas hasta el momento de `PASS`. Cualquier vulnerabilidad de
  memoria o de logging antes de la autenticación FTP potencialmente expone
  ese material si no está estrictamente aislado en memoria y fuera de rutas
  de logging de depuración.

## 8. Riesgos operativos

- **Sin límite explícito de conexiones SSH concurrentes independiente del
  límite de conexiones FTP.** La configuración de ejemplo (§13) solo define
  `maxConnections` para el listener FTP. Si el modelo es "una sesión SSH por
  sesión FTP", ese mismo número determina la carga sobre el servidor SSH
  remoto (que puede tener `MaxSessions`/`MaxStartups` propios). No hay
  coordinación declarada entre ambos límites — riesgo de agotar el servidor
  remoto sin que el gateway lo sepa hasta que empiece a fallar el handshake.
- **Rango de puertos pasivos sin relación explícita con el límite de
  conexiones.** Si `passivePortEnd - passivePortStart` es menor que
  `maxConnections`, las transferencias concurrentes fallarán al no encontrar
  puerto libre, produciendo una degradación confusa de servicio en vez de un
  error de configuración claro. La validación de arranque (§13.1) menciona
  "rango pasivo inválido" pero no especifica esta relación cruzada.
- **Temporales huérfanos tras caída del proceso.** RF-013 cubre limpieza al
  cerrar sesión "cuando sea seguro", pero no hay una política declarada para
  temporales que quedan en el servidor remoto tras un crash del gateway
  (proceso matado, OOM, `SIGKILL`). Se necesita una estrategia de
  reconciliación/barrido al arranque o un job periódico — no está definida
  (relacionado con decisión pendiente #19).
- **Ausencia de almacenamiento de auditoría propio.** Los eventos de
  auditoría (RF-015) se generan pero no se especifica dónde persisten más
  allá de logs estructurados a stdout. Si la plataforma de logs no está
  decidida (decisión pendiente #17), la "auditabilidad" prometida en los
  objetivos depende de infraestructura externa aún no elegida.
- **Un solo proceso, sin HA, con estado de sesión en memoria.** Ya está fuera
  de alcance explícitamente, pero implica que cualquier reinicio (deploy,
  crash, actualización) interrumpe todas las sesiones y transferencias
  activas. Esto es aceptable para el MVP pero debe comunicarse como
  limitación operativa real, no solo como nota de alcance.

## 9. Riesgos de despliegue

- **Relación con `cloudflared` no verificada.** El documento pide
  explícitamente no asumir nombres de contenedores, redes o dominios (§2.4)
  y aun así la arquitectura conceptual depende de esa topología para cumplir
  "no exponer puertos SSH/SFTP directamente". Mientras no se inspeccione la
  configuración real de red, cualquier diagrama de despliegue es
  especulativo.
- **FTP activo/pasivo y NAT/túnel son mutuamente condicionantes.** El modo
  pasivo requiere anunciar una IP/host alcanzable por el cliente y abrir un
  rango de puertos; si el tráfico FTP pasa por un túnel Cloudflare orientado
  a TCP, hay que confirmar que ese túnel soporta múltiples puertos de datos
  dinámicos, no solo el puerto de control. Esto no está resuelto y es
  determinante para si el modo pasivo es viable en la topología real
  (relacionado con decisión pendiente #1, #5, #6, #7).
- **Imagen Docker mínima vs. necesidad de `known_hosts` actualizable.** Un
  runtime mínimo sin herramientas facilita hardening, pero complica rotar
  `known_hosts` o llaves sin reconstruir la imagen si no se monta como volumen
  externo desde el principio.
- **Graceful shutdown con transferencias largas.** Si `maxFileSize` es alto
  (ejemplo: 1 GiB) y el plazo de drenado en `SIGTERM` es corto, transferencias
  legítimas en curso podrían cancelarse en cada despliegue, dejando
  temporales y posibles reintentos manuales. La relación entre plazo de
  shutdown y tamaño máximo de archivo/ancho de banda esperado no está
  calculada.

## 10. Decisiones que permanecen abiertas

Confirmando y sin resolver las 23 preguntas ya listadas en
`FTP2SFTP-REQUIREMENTS.md` §17 (no se repiten aquí en detalle), las que más
condicionan la arquitectura y deberían resolverse primero, porque bloquean
decisiones estructurales tempranas, son:

1. **Modo FTP real de AX (activo/pasivo)** — determina si el módulo `ftpserver`
   necesita soportar `PORT` en absoluto y cómo se diseña el rango pasivo.
2. **Topología de red real (privada vs. túnel vs. mixta)** — determina el
   modelo de amenaza real del canal FTP en texto plano.
3. **Uno vs. varios usuarios FTP, y su relación con cuentas SFTP remotas** —
   determina si `auth`/`authorization`/`filesystem` necesitan resolver un
   mapeo N:1 o N:N desde el día uno o si un solo usuario basta para el MVP.
4. **Sesión SSH por sesión FTP vs. pool** — condiciona el diseño de
   `sshclient`/`sftpclient` y los límites de concurrencia remotos.
5. **Soporte de descarga (`RETR`)** — determina si el flujo de streaming
   inverso es parte del MVP o se difiere.
6. **Captura real de la secuencia de comandos de AX 2012** — bloquea la
   validación de todo el resto (formato de `LIST`, encoding, uso de `TYPE`,
   comandos realmente enviados). Es la única fuente de verdad para
   compatibilidad y hoy no existe.

Las 17 preguntas restantes del documento de requerimientos siguen vigentes y
no se duplican aquí.

---

## 11. Contradicciones detectadas

- **FTPS: "fuera de alcance" vs. "puede evaluarse".** §4 excluye
  explícitamente "Servidor FTPS" del MVP salvo aprobación explícita, mientras
  que §9.5 sugiere "FTPS puede evaluarse si AX lo soporta" como control
  compensatorio a la exposición en texto plano. No es una contradicción dura
  (la segunda mención es condicional y post-MVP), pero convive en tensión con
  la declaración de alcance y debería aclararse si FTPS es una opción real de
  mitigación de seguridad a corto plazo o una idea descartada.
- **"Sin almacenamiento permanente" vs. "archivos temporales cuando el
  streaming no sea suficiente".** §5.1 pide evitar "almacenamiento permanente
  innecesario" y §3.2 permite "uso controlado de archivos temporales" cuando
  el streaming directo no sea seguro o suficiente — pero no se aclara si esos
  temporales son **locales al gateway** (buffer en disco del contenedor) o
  **remotos** (el patrón `.part-<session-id>` de RF-007, que vive en el
  servidor SFTP, no en el gateway). Son dos mecanismos con implicaciones de
  seguridad, capacidad de disco y ciclo de vida completamente distintas, y el
  texto los trata como si fueran la misma cosa. Ver también gap relacionado
  abajo.
- **Health check desacoplado de SFTP vs. objetivo de "operar de forma
  estable".** RF-016 dice explícitamente que la conectividad SFTP "no debe
  bloquear necesariamente" el health check, lo cual es correcto para
  liveness, pero si el mismo criterio se aplicara sin matices a *readiness*
  (RF-017 lo deja "configurable", correctamente) un orquestador podría seguir
  enviando tráfico a una instancia sin conectividad remota. No es una
  contradicción entre requerimientos individuales, pero sí un punto donde
  ambos criterios (health vs. readiness) deben mantenerse estrictamente
  separados en cualquier diseño futuro, porque es fácil fusionarlos por
  simplicidad y violar la intención documentada.

## 12. Huecos en los requerimientos

- No se especifica la **política de sobrescritura** para `STOR` cuando el
  archivo final ya existe (¿se sobrescribe, se rechaza, se versiona?). RF-007
  solo cubre el caso de éxito del rename; el caso "el destino ya existe" no
  tiene política, y como se señaló en §6, el propio protocolo SFTP puede no
  soportar sobrescritura atómica sin una extensión específica del servidor.
- No se define comportamiento para **`ABOR`** (abortar transferencia en
  curso), ausente de la lista de comandos candidatos (§7 RF-004) pese a que
  las pruebas de protocolo sí contemplan "transferencia lenta o interrumpida"
  y "canal de datos que nunca conecta". Si AX puede abortar, el gateway
  necesita manejarlo explícitamente; si no puede, debería documentarse por
  qué se excluye.
- No se define **visibilidad de archivos temporales en `LIST`/`NLST`** (ya
  señalado como riesgo de seguridad en §7).
- No se define la **relación numérica entre `maxConnections` FTP y límite de
  conexiones SSH/SFTP concurrentes** (ya señalado en §8).
- No se define **codificación de nombres de archivo** (ASCII vs. UTF-8) ni si
  el servidor debe anunciar soporte UTF8 en `FEAT` — se delega enteramente a
  "capturar tráfico real de AX" (§5.3), lo cual es razonable pero deja sin
  resolver qué hacer si el comportamiento observado es ambiguo o
  inconsistente entre sesiones.
- No se define un **timeout máximo de transferencia total** (a diferencia de
  timeouts de lectura/escritura individuales, §10.4); una transferencia
  extremadamente lenta pero con actividad intermitente podría nunca disparar
  timeouts de inactividad y ocupar un slot de conexión indefinidamente.
- No se define **quién es dueño del reloj/orden** para `MDTM` cuando hay
  diferencia de zona horaria o de reloj entre el gateway y el servidor
  remoto.
- No se define el **comportamiento ante colisión de nombre temporal** en el
  improbable pero posible caso de reconexión con el mismo `session-id` (por
  ejemplo, si el ID de sesión se reutiliza tras un reinicio del proceso con
  un generador de IDs no verdaderamente único).

## 13. Requisitos implícitos no documentados

- **Un parser de comandos FTP estricto y defensivo** ante líneas
  malformadas, exceso de longitud, o secuencias fuera de la máquina de
  estados esperada (p. ej. `STOR` antes de `PASS`) — se deriva de "denegar
  por defecto" y de las políticas de protocolo en `CLAUDE.md`, pero no está
  como requisito funcional explícito en el documento de requerimientos.
- **Gating de comandos por estado de autenticación**: qué comandos son
  válidos antes de `PASS` exitoso (mínimamente `USER`, `PASS`, `QUIT`, `SYST`,
  `FEAT`, `NOOP`) debe definirse como parte de la máquina de estados de
  sesión, aunque no aparece como ítem propio.
- **Idempotencia/reconciliación de auditoría** ante caída del proceso a
  mitad de una transferencia: un evento de auditoría "iniciado" sin su
  correspondiente "resultado" debe ser interpretable por quien consuma los
  logs — implícito en el objetivo de auditabilidad pero no especificado como
  comportamiento esperado del propio evento.
- **Backoff/límite explícito al reconectar SSH** tras una falla transitoria,
  distinto del backoff de fuerza bruta FTP — se infiere de "no reintentar
  indefinidamente" (RNF-002) pero no se detalla para el lado SFTP
  específicamente.
- **Validación de consistencia entre configuración por usuario** (raíz
  virtual, destino SFTP, permisos) al arranque, no solo validación individual
  de cada campo — por ejemplo, dos usuarios FTP con raíces virtuales que
  mapean a la misma ruta SFTP real, lo cual podría ser intencional o un error
  de configuración; el documento no indica si eso debe rechazarse o
  permitirse.

## 14. Posibles problemas futuros de escalabilidad

- El modelo "una sesión SSH por sesión FTP" escala linealmente con conexiones
  concurrentes tanto en handshakes SSH como en sesiones abiertas en el
  servidor remoto; es aceptable para el volumen actual (una integración AX)
  pero no escala a un escenario con muchos orígenes FTP o alta frecuencia de
  archivos pequeños sin revisar el modelo de conexión (ya anticipado en
  §10.2 de los requerimientos como decisión a revisar "salvo evidencia de
  rendimiento en contra").
- El rango de puertos pasivos fijo acopla la capacidad de concurrencia a la
  configuración de red (firewall/NAT) de forma manual; crecer la
  concurrencia implica coordinación operativa fuera del gateway, no solo
  cambio de configuración.
- La ausencia de un almacén de auditoría propio implica que cualquier
  necesidad futura de consulta/reportería sobre transferencias históricas
  dependerá enteramente de la plataforma de logs elegida más adelante —
  decisión que hoy es un hueco (§17 pendiente #17) y que será costosa de
  cambiar retroactivamente si se elige mal.
- Si en el futuro se necesita soportar más de un servidor SFTP destino por
  gateway (multi-destino), el modelo de configuración estática por usuario
  (RF-014) tendría que extenderse; no es un problema hoy, pero el documento
  ya excluye "replicación automática entre múltiples destinos" del alcance,
  así que cualquier crecimiento en esa dirección es un cambio de alcance, no
  solo de escala.

## 15. Problemas de mantenibilidad

- El doble punto de mapeo de errores (`sftpclient` traduce a dominio,
  `ftpserver`/`errors` traduce a código FTP) señalado en §4 es el riesgo de
  mantenibilidad más concreto: sin una única fuente de verdad para "qué
  código FTP corresponde a qué fallo", el mapeo tenderá a divergir con el
  tiempo entre los dos puntos.
- El módulo `session` describe una responsabilidad amplia ("estado por
  sesión", "operación actual", "referencia controlada a sesión SFTP",
  correlación de auditoría) que puede convertirse fácilmente en un objeto
  dios si `transfer` y `sftpclient` también necesitan escribir en ese estado
  durante una transferencia activa. Los requerimientos no delimitan quién
  tiene permiso de mutar qué campo de la sesión durante una transferencia en
  curso.
- La política de "capturar tráfico real de AX antes de declarar
  compatibilidad" (§5.3, §15.4) es correcta pero implica que partes del
  parser de comandos y del formateador de `LIST` probablemente se
  reescribirán después de la primera prueba real. Vale la pena que quien
  diseñe después sepa que esas dos piezas específicas tienen alta
  probabilidad de cambio y no deberían sobre-invertirse en la primera pasada.

## 16. Posibles errores de diseño a evitar

- **Asumir que "rename" es atómico y soportado universalmente** sin
  verificarlo contra el servidor remoto real antes de comprometerse con la
  estrategia de commit de RF-007 como está descrita (ver §6, §12).
- **Fusionar el modelo de `session` FTP con el ciclo de vida de la conexión
  SSH** de forma tan estrecha que cerrar una fuerce el cierre inmediato de la
  otra sin margen para drenar una transferencia en curso durante shutdown —
  contradiría el flujo de graceful shutdown ya descrito (§14 de
  requerimientos).
- **Tratar la verificación de rutas como un único paso en `filesystem`** sin
  re-validar en el punto de uso dentro de `sftpclient`/`transfer` — la
  normalización debe ser defendida en profundidad (normalizar en el borde,
  pero no confiar ciegamente en que una ruta ya validada sigue siendo segura
  varios pasos después, especialmente si hay symlinks remotos de por medio).
- **Confundir "health" operativo del proceso con "salud" del destino SFTP"**
  en la implementación, a pesar de que el requerimiento los separa
  correctamente a nivel conceptual (ver contradicción discutida en §11).
- **Optimizar prematuramente el pool de conexiones SSH** antes de tener datos
  reales de la carga esperada — los propios requerimientos ya piden
  simplicidad primero (§10.2) y esta es una guía correcta a respetar en el
  diseño, no un error a corregir, pero vale remarcarla porque la tentación de
  "hacerlo bien desde el principio" con un pool es alta en este tipo de
  sistemas.

---

## Siguiente paso sugerido

Antes de diseñar solución, la decisión pendiente #6 (captura real de tráfico
de AX 2012) y la decisión de topología de red (#4/#5) son las que más
condicionan cualquier diseño posterior de `ftpserver` y del modelo de
seguridad. El resto del documento de requerimientos ya es suficientemente
detallado para empezar a diseñar módulos que no dependen de esas dos
incógnitas (p. ej. `config`, `filesystem`, `errors`), pero cualquier diseño
del listener FTP y del modelo pasivo/activo hecho sin esa captura real corre
alto riesgo de reescritura.
