# Portal certificate chain

The Gdańsk portal currently serves a leaf certificate issued by
`Certum DV TLS G2 R39 CA` together with an unrelated legacy intermediate.
Clients which do not fetch missing issuers through AIA cannot build the chain.

`certum-dv-tls-g2-r39-ca.crt` is the missing public intermediate certificate.
The runtime image installs it into the operating-system trust store; TLS
hostname and signature verification remain enabled.

This compatibility certificate can be removed after the portal consistently
serves the correct intermediate and a deployed image succeeds without it.

- Source: `http://certumdvtlsg2r39ca.repository.certum.pl/certumdvtlsg2r39ca.cer`
  (the CA Issuers URL in the portal leaf certificate)
- Subject: `CN=Certum DV TLS G2 R39 CA, O=Asseco Data Systems S.A., C=PL`
- SHA-256 fingerprint: `83:C0:A5:A7:68:44:C8:40:DF:AF:82:0F:FD:02:AD:F6:57:3A:26:82:3E:F6:AF:75:8A:33:84:A0:AC:04:40:83`
- Valid until: 2039-06-05
