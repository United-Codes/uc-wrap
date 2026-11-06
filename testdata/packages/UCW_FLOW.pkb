create or replace package body ucw_flow as

  procedure clamp (p_value in out number, p_min in number, p_max in number)
  is
    c_bad_bounds constant varchar2(40) := 'min must not exceed max';
  begin
    if p_min > p_max then
      raise_application_error(-20100, c_bad_bounds);
    end if;
    p_value := greatest(p_min, least(p_max, p_value));
  end clamp;

  function fizzbuzz (p_upto in pls_integer) return varchar2
  is
    l_out varchar2(4000);
    l_tmp varchar2(10);
  begin
    <<outer_loop>>
    for i in 1 .. p_upto loop
      l_tmp := case
                 when mod(i, 15) = 0 then 'FizzBuzz'
                 when mod(i, 3)  = 0 then 'Fizz'
                 when mod(i, 5)  = 0 then 'Buzz'
                 else to_char(i)
               end;
      l_out := l_out || l_tmp || ' ';
      continue outer_loop when i < 0;
    end loop outer_loop;
    return rtrim(l_out);
  end fizzbuzz;

  function safe_divide (p_a in number, p_b in number) return number
  is
    l_result number;
    l_retries pls_integer := 0;
  begin
    <<retry>>
    begin
      l_result := p_a / p_b;
    exception
      when zero_divide then
        l_retries := l_retries + 1;
        if l_retries < 2 then
          goto retry;
        end if;
        l_result := null;
    end;
    return l_result;
  end safe_divide;

  function run_dynamic (p_literal in number) return number
  is
    l_stmt   varchar2(200) := 'select :1 + 1 from dual';
    l_result number;
  begin
    execute immediate l_stmt into l_result using p_literal;
    return l_result;
  end run_dynamic;

end ucw_flow;
/
