# Requerimientos del Proyecto FTP2SFTP

## 1. Propósito

Desarrollar un servicio backend que funcione como gateway entre un cliente legado que solo soporta FTP y un servidor remoto que únicamente acepta SFTP.

El cliente origen es Microsoft Dynamics AX 2012, que se conecta mediante FTP. El destino es un servidor corporativo accesible mediante SFTP sobre SSH.

El gateway debe:

1. Exponer un servidor FTP compatible con AX 2012.
2. Autenticar al cliente FTP.
3. Recibir archivos y comandos FTP.
4. Traducir las operaciones necesarias a operaciones SFTP.
5. Conectarse al servidor remoto mediante SSH/SFTP.
6. Transferir los archivos de forma segura, íntegra y auditable.
7. Evitar exponer directamente el servidor SFTP al cliente legado.
8. Operar de forma estable como servicio de larga duración.

El proyecto no es un cliente FTP convencional ni un proxy TCP transparente. Es un gateway de aplicación entre dos protocolos diferentes.

## 2. Contexto del sistema

### 2.1 Cliente origen

- Sistema: Microsoft Dynamics AX 2012.
- Protocolo soportado: FTP.
- Puerto esperado originalmente: TCP 21.
- Limitación: AX 2012 no puede conectarse directamente mediante SFTP.
- Operaciones esperadas: autenticación, navegación, creación de directorios cuando esté permitida, subida, consulta, descarga, renombrado o eliminación según el alcance configurado.

### 2.2 Gateway

- Lenguaje preferido: Go.
- Ejecución esperada: Linux.
- Despliegue esperado: contenedor Docker.
- Responsabilidades:
  - implementar el servidor FTP;
  - controlar sesiones FTP;
  - traducir operaciones hacia SFTP;
  - aplicar autenticación, autorización y restricciones de rutas;
  - administrar conexiones SSH/SFTP;
  - registrar eventos operativos y de auditoría;
  - proteger credenciales y llaves;
  - controlar límites, timeouts y recursos.

### 2.3 Servidor destino

- Protocolo: SFTP.
- Transporte: SSH.
- Puerto habitual: TCP 22.
- Autenticación preferida: llave privada; contraseña solo si el entorno lo exige.
- Usuario remoto restringido exclusivamente a SFTP.
- Acceso limitado a una ruta específica.
- La host key del servidor SSH debe validarse.
- Está prohibido desactivar la validación de host keys en producción.

### 2.4 Red

La arquitectura contempla:

- Cloudflare Zero Trust Tunnel para publicar o conectar servicios TCP.
- Un cliente `cloudflared` ejecutándose en Docker.
- Conectividad privada hacia el servidor SFTP.
- Preferencia por no exponer puertos SSH/SFTP directamente a Internet.
- El gateway puede desplegarse en la misma red Docker o en una red con acceso controlado al túnel.

Claude debe inspeccionar la arquitectura real antes de asumir nombres de contenedores, redes, dominios o puertos.

## 3. Objetivos

### 3.1 Objetivo principal

Permitir que AX 2012 continúe operando mediante FTP mientras el servidor destino mantiene SFTP como mecanismo seguro de transferencia.

### 3.2 Objetivos técnicos

- Compatibilidad suficiente con el cliente FTP de AX 2012.
- Traducción confiable de operaciones FTP a SFTP.
- Transferencia mediante streaming cuando sea viable.
- Uso controlado de archivos temporales cuando el streaming directo no sea seguro o suficiente.
- Integridad y trazabilidad de archivos.
- Manejo correcto de fallos parciales y desconexiones.
- Prevención de path traversal y acceso fuera de rutas autorizadas.
- Configuración externa mediante variables de entorno o archivos montados.
- Operación segura dentro de Docker.
- Pruebas unitarias, de integración y de protocolo.
- Transferencias concurrentes bajo límites configurables.

### 3.3 Objetivos operativos

- Inicio y apagado controlados.
- Health y readiness checks.
- Logs estructurados.
- Métricas básicas.
- Correlación entre sesión FTP y operación SFTP.
- Límites configurables.
- Despliegue reproducible.

## 4. Fuera de alcance inicial

Salvo aprobación explícita, no forman parte del MVP:

- Implementación completa de todos los comandos FTP.
- Servidor FTPS.
- Proxy TCP transparente.
- Interfaz gráfica.
- Administración multiempresa avanzada.
- Alta disponibilidad activa-activa.
- Persistencia permanente de archivos dentro del gateway.
- Implementación propia de SSH o criptografía.
- Ejecución arbitraria de comandos SSH.
- Exposición pública directa del servidor SFTP.
- Sincronización bidireccional completa.
- Replicación automática entre múltiples destinos.
- Antivirus integrado.
- Kubernetes.
- Microservicios adicionales sin necesidad demostrada.

## 5. Principios de diseño

### 5.1 Simplicidad

El sistema debe ser un servicio modular y cohesivo.

Evitar:

- microservicios innecesarios;
- abstracciones sin valor;
- factories o repositorios genéricos ceremoniales;
- dependencias de terceros sin revisión;
- almacenamiento permanente innecesario;
- lógica de negocio dentro de handlers de protocolo;
- implementación manual de criptografía.

### 5.2 Seguridad por defecto

- Denegar por defecto.
- Permitir únicamente comandos FTP necesarios.
- Restringir rutas.
- No registrar secretos.
- Validar la identidad del servidor SSH.
- Usar credenciales separadas por entorno.
- No incluir secretos en Git.
- Ejecutar como usuario no root cuando sea posible.
- Aplicar límites de conexiones, sesiones y tamaño.
- Aplicar timeouts a todas las operaciones de red.

### 5.3 Compatibilidad

La compatibilidad con AX 2012 tiene prioridad sobre implementar extensiones FTP no requeridas.

Se debe capturar y documentar:

- secuencia real de comandos FTP enviados por AX;
- modo activo o pasivo;
- formato de rutas;
- comportamiento de subida;
- comandos usados después de subir;
- encoding esperado;
- estrategia de nombres;
- respuestas y códigos FTP esperados.

### 5.4 Operabilidad

Cada error debe permitir identificar:

- sesión FTP;
- usuario;
- operación;
- ruta lógica;
- destino;
- fase de transferencia;
- tipo de fallo;
- duración;
- resultado;

sin exponer contraseñas, llaves, tokens o contenido sensible.

## 6. Arquitectura lógica

La solución debe mantenerse inicialmente como un monolito modular en Go.

### 6.1 Estructura sugerida

```text
cmd/
  ftp2sftp/
    main.go

internal/
  config/
  ftpserver/
  ftpcommand/
  session/
  transfer/
  sftpclient/
  sshclient/
  auth/
  authorization/
  filesystem/
  audit/
  observability/
  health/
  errors/

docs/
  architecture/
  protocols/
  security/
  deployment/

tests/
  integration/
  protocol/
```

La estructura exacta puede adaptarse, pero deben conservarse límites claros.

### 6.2 Responsabilidades principales

#### `ftpserver`

- Escuchar conexiones FTP.
- Controlar el ciclo de vida de cada conexión.
- Parsear comandos.
- Emitir respuestas FTP correctas.
- Administrar canal de control y canal de datos.
- Aplicar timeouts y límites.

#### `session`

- Mantener estado por sesión FTP.
- Usuario autenticado.
- Directorio actual virtual.
- Modo activo o pasivo.
- Estado de transferencia.
- Correlación de auditoría.
- Referencia controlada a una sesión SFTP cuando aplique.

#### `auth`

- Validar credenciales FTP.
- Consultar configuración o almacén autorizado.
- Evitar contraseñas en texto plano.
- Aplicar controles contra fuerza bruta.

#### `authorization`

- Resolver comandos permitidos.
- Resolver rutas permitidas.
- Impedir acceso fuera de la raíz virtual.
- Aplicar permisos de lectura, escritura, renombrado y borrado.

#### `filesystem`

- Normalizar rutas FTP.
- Mapear rutas virtuales a rutas SFTP.
- Bloquear `..`, rutas absolutas no permitidas, traversal y escapes mediante enlaces simbólicos.
- Mantener una raíz virtual por usuario o configuración.

#### `sshclient`

- Establecer conexiones SSH.
- Validar host key.
- Manejar autenticación.
- Aplicar algoritmos permitidos y timeout.
- Cerrar recursos correctamente.

#### `sftpclient`

- Abrir canal SFTP.
- Ejecutar operaciones remotas.
- Traducir errores SFTP a errores de dominio.
- No ejecutar shell remoto.
- Administrar reutilización o aislamiento de sesiones según el diseño aprobado.

#### `transfer`

- Orquestar subidas y descargas.
- Aplicar streaming o almacenamiento temporal.
- Controlar backpressure.
- Detectar transferencias parciales.
- Aplicar estrategia de commit atómico.
- Limpiar archivos incompletos.
- Generar eventos de auditoría.

## 7. Requerimientos funcionales

### RF-001: Escucha FTP

El gateway debe escuchar conexiones FTP en una dirección y puerto configurables.

Configuración mínima:

- host de escucha;
- puerto de control;
- rango pasivo;
- IP o hostname anunciado en modo pasivo;
- límite de conexiones;
- timeouts.

### RF-002: Autenticación FTP

El sistema debe:

- aceptar usuario y contraseña FTP;
- rechazar credenciales inválidas;
- impedir enumeración evidente de usuarios;
- aplicar límite o retraso progresivo ante intentos fallidos;
- registrar el resultado sin registrar la contraseña.

### RF-003: Sesión FTP

Cada conexión debe mantener:

- identificador de sesión;
- usuario;
- directorio actual;
- modo de transferencia;
- configuración activa/pasiva;
- operación actual;
- instante de inicio;
- actividad reciente.

### RF-004: Comandos FTP

El gateway debe soportar al menos los comandos que AX utilice realmente.

Candidatos iniciales:

- `USER`
- `PASS`
- `SYST`
- `FEAT`
- `PWD`
- `CWD`
- `CDUP`
- `TYPE`
- `PASV`
- `EPSV`
- `PORT`, solo si es necesario
- `LIST`
- `NLST`
- `SIZE`
- `MDTM`
- `STOR`
- `RETR`, si se requiere descarga
- `MKD`
- `RMD`, si se permite
- `DELE`, si se permite
- `RNFR`
- `RNTO`
- `NOOP`
- `QUIT`

No implementar comandos sin caso de uso o necesidad de compatibilidad.

### RF-005: Mapeo de rutas

Las rutas FTP deben mapearse a rutas SFTP mediante configuración explícita.

```text
FTP virtual: /facturas/2026/archivo.xml
SFTP real:   /home/briva.mx/public_html/guias/facturas/2026/archivo.xml
```

Nunca debe permitirse escapar de la raíz remota configurada.

### RF-006: Subida de archivos

Para `STOR`, el gateway debe:

1. validar autenticación;
2. validar autorización;
3. normalizar la ruta;
4. abrir canal de datos FTP;
5. abrir destino SFTP temporal;
6. transferir mediante streaming;
7. controlar backpressure;
8. detectar EOF correcto;
9. cerrar ambos extremos;
10. verificar tamaño cuando sea posible;
11. renombrar el archivo temporal al nombre definitivo;
12. registrar el resultado;
13. limpiar temporales si ocurre un error.

### RF-007: Commit atómico

La subida debe preferir un nombre temporal:

```text
archivo.xml.part-<session-id>
```

Y al finalizar correctamente:

```text
rename archivo.xml.part-<session-id> -> archivo.xml
```

La atomicidad real depende del servidor y filesystem SFTP y debe verificarse.

### RF-008: Descarga

Si se habilita `RETR`, el gateway debe transmitir desde SFTP al canal FTP sin cargar el archivo completo en memoria.

### RF-009: Listado

Los comandos de listado deben devolver respuestas compatibles con AX 2012. El formato debe validarse contra el cliente real.

### RF-010: Renombrado

Si se habilita `RNFR`/`RNTO`:

- ambas operaciones pertenecen a la misma sesión;
- se validan rutas;
- se impiden escapes;
- se registran origen y destino;
- se manejan conflictos;
- se traducen errores correctamente.

### RF-011: Eliminación

La eliminación estará deshabilitada por defecto. Cuando se habilite:

- debe configurarse por usuario o ruta;
- requiere autorización explícita;
- queda auditada;
- no puede seguir enlaces fuera de la raíz permitida.

### RF-012: Creación de directorios

Debe respetar la raíz virtual, validar rutas, definir permisos remotos y registrar el resultado.

### RF-013: Cierre de sesión

Al finalizar una sesión se deben cerrar:

- socket de control;
- socket de datos;
- archivos abiertos;
- canal SFTP;
- sesión SSH si no se reutiliza;
- goroutines asociadas;
- temporales abandonados cuando sea seguro.

### RF-014: Configuración por usuario

Debe ser posible configurar:

- credencial FTP;
- raíz virtual;
- destino SFTP;
- usuario SFTP;
- mecanismo de autenticación SSH;
- comandos permitidos;
- permisos de lectura, escritura y borrado;
- límite de tamaño;
- límite de concurrencia.

La primera versión puede usar configuración estática segura.

### RF-015: Auditoría

Cada operación relevante debe generar un evento estructurado con:

- timestamp;
- sessionId;
- transferId;
- ftpUser;
- clientIp;
- command;
- virtualPath;
- operation;
- bytes;
- duration;
- result;
- errorCode;
- remoteHost alias;
- checksum, si se calcula.

### RF-016: Health check

Debe permitir verificar:

- proceso activo;
- listener FTP activo;
- configuración válida;
- capacidad básica de crear sesiones internas.

La conectividad SFTP no debe bloquear necesariamente el health check del proceso.

### RF-017: Readiness

Debe indicar si el gateway está listo para aceptar operaciones. La política respecto al servidor SFTP debe ser configurable.

### RF-018: Herramientas de operador (CLI)

El mismo binario del gateway debe ofrecer subcomandos de apoyo operativo
para preparar y verificar una configuración sin necesidad de arrancar el
listener FTP:

- **Validación de configuración**: cargar y validar un archivo de
  configuración, reportando todos los problemas encontrados con el mismo
  código de validación que usa el arranque real (nunca una
  reimplementación separada que pueda desalinearse).
- **Generación de hash de contraseña FTP**: producir un hash bcrypt
  apto para `users[].passwordHash` a partir de una contraseña leída de
  forma segura (sin eco en terminal cuando hay TTY disponible; nunca
  aceptada como argumento de línea de comandos, para no quedar expuesta
  en el historial de shell ni en la lista de procesos).
- **Verificación de conectividad SFTP**: para uno o todos los usuarios
  configurados, abrir una conexión SSH/SFTP real de corta duración
  (verificación de host key incluida) y reportar éxito o fallo por
  usuario, reutilizando el mismo código que ejecuta el chequeo de
  *readiness* (RF-017).

Estos subcomandos son utilidades de preparación/diagnóstico para el
operador que despliega el gateway, no una superficie de administración
remota ni un servicio adicional: no exponen red, no persisten estado
propio y no requieren el proceso del gateway en ejecución. No sustituyen
ni amplían el alcance excluido en la sección 4 (sin interfaz gráfica, sin
panel de administración).

## 8. Requerimientos no funcionales

### RNF-001: Seguridad

- No ejecutar como root salvo restricción documentada.
- No almacenar secretos en el repositorio.
- No registrar secretos.
- Validar host keys.
- Restringir rutas.
- Aplicar límites y timeouts.
- Aplicar permisos mínimos.
- Usar imágenes Docker mínimas.
- Mantener dependencias revisadas.

### RNF-002: Rendimiento

- Soportar múltiples sesiones concurrentes bajo límites configurables.
- No cargar archivos completos en memoria.
- No crear goroutines sin límite.
- No mantener buffers ilimitados.
- No abrir conexiones SSH sin control.
- No reintentar indefinidamente.

Los objetivos concretos de throughput y concurrencia deben definirse con datos reales.

### RNF-003: Confiabilidad

- Toda operación de red debe tener timeout o deadline.
- Las desconexiones deben liberar recursos.
- Los archivos parciales deben identificarse.
- Los fallos transitorios no deben corromper archivos.
- Los reintentos solo se aplican a operaciones idempotentes o protegidas contra duplicados.

### RNF-004: Mantenibilidad

- Código idiomático de Go.
- Paquetes pequeños y cohesivos.
- Interfaces solo para límites reales.
- Errores con contexto.
- Dependencias justificadas.
- Configuración validada al inicio.
- ADR para decisiones importantes.

### RNF-005: Portabilidad

El servicio debe poder ejecutarse en Linux, Docker y WSL para desarrollo.

### RNF-006: Observabilidad

- Logs estructurados.
- Correlation IDs.
- Métricas de conexiones, autenticación, transferencias, errores, duración y bytes.
- Métricas de conexiones SSH activas y temporales pendientes.

### RNF-007: Compatibilidad

La compatibilidad debe probarse contra el cliente FTP real de AX 2012. Probar únicamente con FileZilla no es suficiente.

## 9. Seguridad detallada

### 9.1 Credenciales FTP

La contraseña FTP no debe almacenarse en texto plano. Usar hash seguro o un almacén autorizado.

### 9.2 Credenciales SFTP

Preferir llave privada. La llave debe:

- almacenarse fuera de la imagen;
- montarse como secreto;
- tener permisos restrictivos;
- poder rotarse;
- no registrarse;
- no exponerse al usuario FTP.

### 9.3 Host keys

Debe existir `known_hosts` o mecanismo equivalente. Está prohibido aceptar cualquier host key en producción.

### 9.4 Path traversal

Antes de operar sobre una ruta:

1. normalizar separadores;
2. limpiar segmentos;
3. resolver ruta virtual;
4. validar que permanezca bajo la raíz;
5. impedir rutas inválidas;
6. considerar enlaces simbólicos remotos;
7. aplicar autorización.

### 9.5 Exposición FTP

FTP transmite credenciales y datos sin cifrado. Por ello:

- el listener debe limitarse a red interna o túnel controlado;
- no debe exponerse públicamente sin una decisión de riesgo explícita;
- deben documentarse controles compensatorios;
- FTPS puede evaluarse si AX lo soporta.

### 9.6 Fuerza bruta

Implementar o planear:

- límite por IP;
- límite por usuario;
- retraso progresivo;
- bloqueo temporal configurable;
- métricas y alertas.

### 9.7 Denegación de servicio

Controlar:

- conexiones simultáneas;
- sesiones por IP;
- transferencias por usuario;
- tamaño máximo;
- tiempo inactivo;
- buffers;
- goroutines;
- conexiones SSH;
- archivos temporales;
- ancho de banda si es necesario.

## 10. Manejo de conexiones

### 10.1 FTP

Cada sesión debe manejar el canal de control, un canal de datos activo como máximo, estado de autenticación, directorio virtual, timeout de inactividad y cierre limpio.

### 10.2 SSH/SFTP

Debe decidirse explícitamente entre:

#### Sesión SSH por sesión FTP

Ventajas: aislamiento, simplicidad y cierre natural.

Desventajas: mayor costo de handshake, latencia y conexiones simultáneas.

#### Reutilización o pool

Ventajas: menor latencia y menos handshakes.

Desventajas: mayor complejidad, sesiones inválidas, límites de canales, concurrencia y aislamiento.

Para el MVP se prefiere simplicidad y aislamiento, salvo evidencia de rendimiento en contra.

### 10.3 Backpressure

El flujo debe copiar datos con buffers acotados. El lector no debe producir datos indefinidamente cuando el escritor remoto esté bloqueado.

### 10.4 Timeouts

Deben ser configurables:

- autenticación FTP;
- inactividad de sesión;
- espera de canal de datos;
- conexión y handshake SSH;
- apertura de SFTP;
- lectura;
- escritura;
- operación remota;
- graceful shutdown.

## 11. Manejo de errores

Debe existir una taxonomía para:

- configuración inválida;
- autenticación fallida;
- autorización fallida;
- ruta inválida;
- comando no soportado;
- conflicto de archivo;
- conexión SSH fallida;
- host key inválida;
- autenticación SFTP fallida;
- timeout;
- desconexión;
- escritura parcial;
- almacenamiento remoto lleno;
- permiso remoto denegado;
- error interno.

Los errores internos deben mapearse a códigos FTP sin filtrar detalles sensibles.

Mapeo conceptual:

```text
Autenticación fallida             -> 530
Ruta no encontrada o denegada     -> 550
Servicio temporal no disponible   -> 421 o 451
Error durante transferencia       -> 426 o 451
Comando no soportado              -> 502
```

El mapeo final debe validarse contra AX.

## 12. Integridad de archivos

Cada transferencia debe registrar al menos nombre, tamaño, resultado y duración.

Cuando se requiera mayor garantía:

- calcular SHA-256 durante el streaming;
- comparar con un valor esperado si existe;
- registrar el hash sin sustituir la atomicidad;
- evitar releer archivos grandes innecesariamente.

El sistema debe distinguir entre transferencia recibida, escrita, cerrada, renombrada y confirmada.

## 13. Configuración

La configuración será externa al binario.

Ejemplo conceptual:

```yaml
server:
  listenAddress: "0.0.0.0"
  controlPort: 21
  passiveAddress: "ftp.internal.example"
  passivePortStart: 30000
  passivePortEnd: 30100
  maxConnections: 50
  idleTimeout: "5m"

sftp:
  host: "sftp.internal.example"
  port: 22
  user: "admin_facturas"
  privateKeyFile: "/run/secrets/sftp_private_key"
  knownHostsFile: "/app/config/known_hosts"
  rootPath: "/home/briva.mx/public_html/guias/facturas"
  connectTimeout: "10s"

transfer:
  maxFileSize: 1073741824
  bufferSize: 65536
  temporarySuffix: ".part"
  calculateSha256: true

observability:
  logFormat: "json"
  logLevel: "info"
```

Los valores no son definitivos.

### 13.1 Validación al inicio

El servicio no debe iniciar si:

- falta configuración obligatoria;
- el rango pasivo es inválido;
- `known_hosts` no existe;
- existen usuarios duplicados;
- una raíz virtual es insegura;
- los límites son inconsistentes.

## 14. Despliegue Docker

### 14.1 Imagen

- Build multi-stage.
- Binario Go compilado.
- Runtime mínimo.
- Usuario no root.
- Sin herramientas innecesarias.
- Sin secretos.
- Certificados CA disponibles.
- Health check cuando aplique.

### 14.2 Redes

Documentar:

- origen de conexiones FTP;
- salida hacia SFTP;
- relación con `cloudflared`;
- puertos publicados;
- rango pasivo;
- reglas firewall;
- resolución DNS.

### 14.3 Persistencia

Persistir solo configuración no secreta, `known_hosts`, temporales cuando sean necesarios y logs si no se envían a stdout.

Las llaves deben montarse como secretos o archivos de solo lectura.

### 14.4 Graceful shutdown

Al recibir `SIGTERM`:

1. dejar de aceptar conexiones;
2. permitir completar transferencias dentro de un plazo;
3. cancelar las restantes al expirar;
4. cerrar canales de datos;
5. cerrar sesiones SFTP/SSH;
6. limpiar o marcar temporales;
7. vaciar logs y métricas;
8. finalizar.

## 15. Pruebas

### 15.1 Unitarias

- normalización y mapeo de rutas;
- autorización;
- mapeo de errores;
- parsing de comandos;
- configuración;
- límites;
- clasificación de errores.

### 15.2 Integración

Probar contra un servidor SFTP controlado:

- conexión;
- host key válida e inválida;
- autenticación;
- subida;
- descarga;
- listado;
- rename;
- permisos;
- desconexión;
- timeout;
- archivo parcial.

### 15.3 Protocolo FTP

Probar:

- login;
- modo pasivo;
- modo activo si se soporta;
- respuestas;
- comandos inválidos;
- desconexión abrupta;
- canal de datos que nunca conecta;
- transferencia lenta o interrumpida;
- múltiples sesiones;
- límites.

### 15.4 Compatibilidad AX 2012

Registrar una sesión real para identificar comandos, orden, rutas, modo activo/pasivo, respuestas esperadas, encoding y comportamiento después de `STOR`.

Esta prueba es obligatoria antes de declarar compatibilidad.

### 15.5 Concurrencia

- múltiples subidas;
- mismo nombre simultáneo;
- distintos usuarios;
- límite de sesiones;
- cierre durante transferencia;
- race detector.

```bash
go test -race ./...
```

### 15.6 Seguridad

- traversal;
- rutas absolutas;
- enlaces simbólicos;
- credenciales inválidas;
- brute force;
- comandos deshabilitados;
- lectura o escritura fuera de raíz;
- host key inesperada;
- archivos demasiado grandes;
- agotamiento de conexiones.

## 16. Métricas sugeridas

- `ftp_connections_total`
- `ftp_connections_active`
- `ftp_auth_attempts_total`
- `ftp_auth_failures_total`
- `ftp_commands_total`
- `ftp_sessions_duration_seconds`
- `transfer_total`
- `transfer_active`
- `transfer_bytes_total`
- `transfer_duration_seconds`
- `transfer_failures_total`
- `sftp_connections_active`
- `sftp_connection_failures_total`
- `sftp_operation_duration_seconds`
- `temporary_files_pending`
- `rate_limit_rejections_total`

## 17. Decisiones pendientes

Claude debe identificar y no inventar:

1. ¿AX utiliza modo activo, pasivo o ambos?
2. ¿AX requiere únicamente subida o también descarga?
3. ¿Qué comandos FTP ejecuta realmente?
4. ¿El listener FTP estará solo en red privada?
5. ¿Se utilizará Cloudflare Tunnel para el tráfico FTP?
6. ¿Cómo se anunciará la dirección PASV?
7. ¿Cuál será el rango de puertos pasivos?
8. ¿Cuántos usuarios FTP existirán?
9. ¿Cada usuario tendrá un usuario SFTP distinto?
10. ¿Se usará una conexión SSH por sesión o reutilización?
11. ¿Cuál es el tamaño máximo esperado?
12. ¿Cuántas transferencias concurrentes existen?
13. ¿Se requiere sobrescritura, borrado o rename?
14. ¿Se necesita calcular hash?
15. ¿Cómo se almacenarán las credenciales FTP?
16. ¿Qué secret manager o montaje se usará?
17. ¿Qué formato de logs y plataforma de monitoreo se utilizará?
18. ¿Cuál es la política de reintentos?
19. ¿Qué pasará con temporales abandonados?
20. ¿Cuál es el SLA esperado?
21. ¿Existe requisito de alta disponibilidad?
22. ¿Qué versión mínima de Go se utilizará?
23. ¿Qué dependencias externas se aceptarán?

## 18. Política de dependencias

Existe preferencia por minimizar dependencias externas. Sin embargo:

- no se debe implementar criptografía desde cero;
- no se debe implementar SSH desde cero para producción;
- no se debe implementar un protocolo complejo desde cero sin evaluar costo, compatibilidad y riesgo;
- las dependencias deben fijarse por versión;
- debe revisarse mantenimiento, licencia, seguridad y alcance;
- deben aislarse detrás de límites internos cuando tenga valor.

Claude debe diferenciar entre prototipo educativo, MVP operativo e implementación apta para producción.

## 19. Criterios de aceptación del MVP

1. AX 2012 puede conectarse al listener FTP.
2. AX puede autenticarse.
3. AX puede entrar a la ruta autorizada.
4. AX puede subir un archivo.
5. El archivo llega al destino mediante SFTP.
6. El archivo no queda visible con su nombre final mientras está incompleto.
7. Una transferencia fallida no deja un archivo final corrupto.
8. Las rutas no pueden escapar de la raíz autorizada.
9. La host key SSH se valida.
10. Las credenciales SFTP no se exponen al cliente FTP.
11. Los secretos no están en Git ni en la imagen.
12. Existen timeouts y límites.
13. Los recursos se liberan al desconectarse.
14. El servicio soporta graceful shutdown.
15. Existen logs correlacionados.
16. Existen pruebas unitarias y de integración.
17. La prueba real con AX 2012 es exitosa.
18. El despliegue Docker es reproducible.
19. Existe documentación de configuración y operación.
20. Los riesgos restantes están documentados.

## 20. Criterios de aceptación para producción

Además del MVP:

- hardening de contenedor;
- usuario no root;
- secretos gestionados externamente;
- métricas y alertas;
- límites validados con carga;
- recuperación documentada;
- rotación de llaves;
- runbook operativo;
- estrategia de actualización y rollback probado;
- pruebas de seguridad;
- auditoría de dependencias;
- pruebas de interrupción y recuperación;
- política de limpieza de temporales;
- diagnóstico extremo a extremo de transferencias.

## 21. Instrucciones para Claude

Al trabajar en este proyecto, Claude debe:

1. Leer este archivo antes de diseñar cambios importantes.
2. Inspeccionar únicamente los módulos relevantes.
3. No asumir que FTP y SFTP son equivalentes.
4. No proponer un proxy TCP transparente.
5. No desactivar validación de host keys.
6. No almacenar secretos en código.
7. No recomendar microservicios sin necesidad.
8. No crear abstracciones prematuras.
9. Mantener explícito el ciclo de vida de conexiones y goroutines.
10. Analizar backpressure, timeouts, cancelación y graceful shutdown.
11. Analizar canales FTP de control y datos.
12. Considerar NAT, firewall, túneles y rango pasivo.
13. Basar compatibilidad en el comportamiento real de AX 2012.
14. Separar autenticación, autorización y mapeo de rutas.
15. Evitar cargar archivos completos en memoria.
16. Proponer commit atómico para subidas.
17. Analizar fallos parciales.
18. Ejecutar pruebas relevantes.
19. Revisar el diff.
20. Reportar supuestos, riesgos y decisiones pendientes.

Para cambios significativos, la respuesta debe incluir:

- estado actual;
- supuestos;
- diseño;
- alternativas;
- riesgos;
- archivos afectados;
- pruebas;
- despliegue;
- decisiones pendientes.

## 22. Resultado esperado

El producto final debe ser un gateway FTP a SFTP:

- seguro;
- limitado;
- auditable;
- compatible con AX 2012;
- operativo en Linux y Docker;
- mantenible en Go;
- consciente del ciclo de vida de red;
- resistente a fallos parciales;
- desplegable sin exponer innecesariamente el servidor SFTP.
