# Tencent host nginx

These host-level nginx configs terminate TLS on the Tencent CVM and proxy to
the Docker stacks bound to `127.0.0.1`.

The current certificate staged on the Tencent CVM covers:

- `api.catsco.cc`
- `app.catsco.cc`

It does not cover `catsco.cc` or `www.catsco.cc`. Until a wildcard/root-domain
certificate is installed, root-domain HTTPS should not be advertised. HTTP
requests for `catsco.cc` and `www.catsco.cc` can redirect to
`https://app.catsco.cc`.

Install without enabling traffic:

```bash
sudo install -o root -g root -m 644 deploy/tencent/nginx/catscompany-app.conf /etc/nginx/sites-available/catscompany-app
sudo install -o root -g root -m 644 deploy/tencent/nginx/catscompany-api.conf /etc/nginx/sites-available/catscompany-api
sudo nginx -t
```

Enable during cutover:

```bash
sudo ln -sfn /etc/nginx/sites-available/catscompany-app /etc/nginx/sites-enabled/catscompany-app
sudo ln -sfn /etc/nginx/sites-available/catscompany-api /etc/nginx/sites-enabled/catscompany-api
sudo nginx -t
sudo systemctl reload nginx
```
