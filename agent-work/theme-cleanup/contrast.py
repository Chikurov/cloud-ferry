def srgb(c):
    c = c / 255.0
    return c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4

def lum(hexcolor):
    h = hexcolor.lstrip('#')
    if len(h) == 3: h = ''.join(ch * 2 for ch in h)
    r, g, b = (int(h[i:i+2], 16) for i in (0, 2, 4))
    return 0.2126 * srgb(r) + 0.7152 * srgb(g) + 0.0722 * srgb(b)

def contrast(fg, bg):
    l1, l2 = lum(fg), lum(bg)
    hi, lo = max(l1, l2), min(l1, l2)
    return (hi + 0.05) / (lo + 0.05)

def blend(fg, bg, alpha):
    f = fg.lstrip('#'); b = bg.lstrip('#')
    fr, fg_, fb = (int(f[i:i+2], 16) for i in (0, 2, 4))
    br, bg_, bb = (int(b[i:i+2], 16) for i in (0, 2, 4))
    out = tuple(round(a * alpha + c * (1 - alpha)) for a, c in zip((fr, fg_, fb), (br, bg_, bb)))
    return '#%02x%02x%02x' % out

light = dict(page='#f5f4f8', surface='#ffffff', sunken='#f2f0f5', ink='#26222e', ink_soft='#4b4555',
             muted='#655e70', accent='#684c96', accent_hover='#553d80', ok='#21704d', warn='#795600',
             err='#a72b3d', amber='#825319', border_control='#94909b', hairline='#dedbe5', on_accent='#ffffff')
dark = dict(page='#16151a', surface='#222127', sunken='#2c2932', ink='#f3eff9', ink_soft='#dad3e2',
            muted='#b9b1c4', accent='#b69add', accent_hover='#c7abea', ok='#96d2ac', warn='#ebcc8d',
            err='#ff9ca9', amber='#ecc68e', border_control='#777080', hairline='#39353f', on_accent='#201827')

for name, t in (('LIGHT', light), ('DARK', dark)):
    print('==', name)
    surfaces = [('surface', t['surface']), ('page', t['page']), ('sunken', t['sunken'])]
    for label, token in [('ink', t['ink']), ('ink_soft', t['ink_soft']), ('muted', t['muted']),
                         ('accent', t['accent']), ('ok', t['ok']), ('warn', t['warn']), ('err', t['err']),
                         ('amber', t['amber'])]:
        for sname, s in surfaces:
            r = contrast(token, s)
            flag = 'OK ' if r >= 4.5 else 'FAIL'
            print(f'{flag} {label:10s} on {sname:8s} {r:5.2f}')
    for sname, s in surfaces:
        r = contrast(t['border_control'], s)
        print(f'{"OK " if r >= 3 else "FAIL"} border_control on {sname:8s} {r:5.2f}')
    r = contrast(t['on_accent'], t['accent'])
    print(f'{"OK " if r >= 4.5 else "FAIL"} on_accent on accent {r:5.2f}')
    soft = blend(t['accent'], t['surface'], 0.13)
    for label, token in [('accent', t['accent']), ('ink', t['ink']), ('muted', t['muted'])]:
        r = contrast(token, soft)
        print(f'{"OK " if r >= 4.5 else "FAIL"} {label} on accent-soft {soft} {r:5.2f}')
    print('hairline on page', round(contrast(t['hairline'], t['page']), 2))
